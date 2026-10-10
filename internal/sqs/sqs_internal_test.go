package sqs

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/nexrad"
	"github.com/puzpuzpuz/xsync/v4"
)

const ktlx = "KTLX"

func sites() *xsync.MapOf[string, uint] {
	return xsync.NewMapOf[string, uint]()
}

func TestListenRefcountsPerStation(t *testing.T) {
	t.Parallel()
	m := sites()

	listen(m, ktlx)
	listen(m, ktlx)
	if got := subscribedSites(m); len(got) != 1 || got[0] != ktlx {
		t.Fatalf("two listens should name the site once, got %v", got)
	}

	unlisten(m, ktlx)
	if got := subscribedSites(m); len(got) != 1 {
		t.Fatalf("one of two listeners left, site should still be subscribed, got %v", got)
	}

	unlisten(m, ktlx)
	if got := subscribedSites(m); len(got) != 0 {
		t.Fatalf("last listener gone, site should be unsubscribed, got %v", got)
	}
	if _, ok := m.Load(ktlx); ok {
		t.Fatal("last listener gone, entry should be deleted rather than left at zero")
	}
}

// Decrementing a station nobody is listening to used to underflow uint to
// 1<<64-1, leaving an entry that subscribedSites would then treat as live.
func TestUnlistenUnknownStationDoesNotUnderflow(t *testing.T) {
	t.Parallel()
	m := sites()

	unlisten(m, ktlx)

	if v, ok := m.Load(ktlx); ok {
		t.Fatalf("unlisten of an absent station left an entry: %d", v)
	}
	if got := subscribedSites(m); len(got) != 0 {
		t.Fatalf("unlisten of an absent station subscribed it: %v", got)
	}

	// And the entry it used to leave behind must not resurrect the site on a
	// later decrement either.
	unlisten(m, ktlx)
	if got := subscribedSites(m); len(got) != 0 {
		t.Fatalf("second unlisten subscribed the station: %v", got)
	}
}

func TestSubscribedSitesIsSortedAndSkipsZero(t *testing.T) {
	t.Parallel()
	m := sites()
	listen(m, ktlx)
	listen(m, "KABC")
	listen(m, "KOUN")
	m.Store("KZZZ", 0)

	got := subscribedSites(m)
	want := []string{"KABC", "KOUN", ktlx}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestChunkFilterPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		sites []string
		want  string
	}{
		{"no sites matches nothing", nil, `{"SiteID": ["nonsense"]}`},
		{"one site", []string{ktlx}, `{"SiteID": ["KTLX"]}`},
		{"many sites", []string{"KABC", ktlx}, `{"SiteID": ["KABC","KTLX"]}`},
	} {
		got, _, err := chunkFilterPolicy(tc.sites)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

// The archive topic publishes raw S3 events with no message attributes, so this
// policy is matched against the body, and the object key is the only field that
// names the site.
func TestArchiveFilterPolicy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 7, 23, 42, 0, 0, time.UTC)

	got, collapsed, err := archiveFilterPolicy([]string{ktlx}, now)
	if err != nil {
		t.Fatal(err)
	}
	if collapsed {
		t.Fatal("one site should be named individually")
	}
	want := `{"Records":{"s3":{"object":{"key":[` +
		`{"prefix":"2026/09/07/KTLX/"},{"prefix":"2026/09/08/KTLX/"}]}}}}`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}

	// An empty subscription list must still match nothing.
	got, _, err = archiveFilterPolicy(nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/nonsense/") {
		t.Errorf("no sites should render a filter that matches nothing, got %s", got)
	}
}

// The next UTC day is named alongside the current one so a volume filed either
// side of midnight matches without waiting for the refresh to come round.
func TestArchiveFilterPolicyStraddlesTheDateRoll(t *testing.T) {
	t.Parallel()
	got, _, err := archiveFilterPolicy(
		[]string{ktlx}, time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2026/12/31/KTLX/", "2027/01/01/KTLX/"} {
		if !strings.Contains(got, date) {
			t.Errorf("policy should name %s, got %s", date, got)
		}
	}
}

// A key is dated by the volume's start, and the volume is filed minutes after
// it ends. Just past midnight the volume begun before it is still on its way,
// so a policy written then has to keep naming the day that just ended, or the
// last volume of every day is lost whenever a refresh or a connect lands in
// that gap.
func TestArchiveFilterPolicyKeepsYesterdayJustAfterMidnight(t *testing.T) {
	t.Parallel()
	got, _, err := archiveFilterPolicy(
		[]string{ktlx}, time.Date(2027, 1, 1, 0, 5, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2026/12/31/KTLX/", "2027/01/01/KTLX/"} {
		if !strings.Contains(got, date) {
			t.Errorf("policy should name %s, got %s", date, got)
		}
	}
}

// The defect this replaced: `*/SITE/*` is two wildcards, SNS scores wildcard
// complexity across the whole policy, and it refused the write from the fifth
// site on -- which failed the websocket connect that triggered it.
func TestArchiveFilterPolicyUsesNoWildcards(t *testing.T) {
	t.Parallel()
	sites := make([]string, 0, 40)
	for i := range 40 {
		sites = append(sites, fmt.Sprintf("K%03d", i))
	}
	for _, n := range []int{1, 4, 5, 6, 12, 18, 19, 40} {
		got, _, err := archiveFilterPolicy(sites[:n], time.Now())
		if err != nil {
			t.Fatalf("%d sites: %v", n, err)
		}
		if strings.Contains(got, "wildcard") || strings.Contains(got, "*") {
			t.Errorf("%d sites: policy contains a wildcard, which SNS refuses in bulk: %s", n, got)
		}
	}
}

// A policy SNS will refuse is worse than a wide one: the write fails, so the
// filter keeps whatever it had. Measured against live SNS: 36 values
// accepted, 40 refused as "Filter policy is too complex".
func TestArchiveFilterPolicyStaysInsideWhatSNSAccepts(t *testing.T) {
	t.Parallel()
	sites := make([]string, 0, 200)
	for i := range 200 {
		sites = append(sites, fmt.Sprintf("K%03d", i))
	}
	for _, n := range []int{1, 6, 17, 18, 19, 36, 37, 100, 200} {
		policy, _, err := archiveFilterPolicy(sites[:n], time.Now())
		if err != nil {
			t.Fatalf("%d sites: %v", n, err)
		}
		values := strings.Count(policy, `{"prefix":`)
		if values > maxArchiveFilterValues {
			t.Errorf("%d sites rendered %d values, over the %d SNS accepts",
				n, values, maxArchiveFilterValues)
		}
	}
}

func archivePrefixes(t *testing.T, policy string) []string {
	t.Helper()
	var parsed struct {
		Records struct {
			S3 struct {
				Object struct {
					Key []struct {
						Prefix string `json:"prefix"`
					} `json:"key"`
				} `json:"object"`
			} `json:"s3"`
		} `json:"Records"`
	}
	if err := json.Unmarshal([]byte(policy), &parsed); err != nil {
		t.Fatalf("policy is not the expected shape: %v\n%s", err, policy)
	}
	prefixes := make([]string, 0, len(parsed.Records.S3.Object.Key))
	for _, k := range parsed.Records.S3.Object.Key {
		prefixes = append(prefixes, k.Prefix)
	}
	return prefixes
}

// Past 36 values the sites at the end of the alphabet used to be cut from the
// policy, so every Alaska, Hawaii, Puerto Rico and TDWR subscriber went silent
// once enough stations were in use. Every subscribed site has to stay matched,
// even if that means the filter lets neighbours through as well.
func TestArchiveFilterPolicyMatchesEverySubscribedSite(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	all := nexrad.Stations()
	for _, n := range []int{1, 18, 19, 36, 37, 60, 100, len(all)} {
		subscribed := all[len(all)-n:]
		policy, collapsed, err := archiveFilterPolicy(subscribed, now)
		if err != nil {
			t.Fatalf("%d sites: %v", n, err)
		}
		prefixes := archivePrefixes(t, policy)
		if len(prefixes) > maxArchiveFilterValues {
			t.Errorf("%d sites rendered %d values, over the %d SNS accepts",
				n, len(prefixes), maxArchiveFilterValues)
		}
		if collapsed != (n > maxArchiveFilterValues) {
			t.Errorf("%d sites: collapsed = %v", n, collapsed)
		}
		for _, site := range subscribed {
			key := now.Format("2006/01/02") + "/" + site + "/" + site + now.Format("20060102_150405") + "_V06"
			if !slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(key, p) }) {
				t.Errorf("%d sites: %s is not matched by %v", n, site, prefixes)
			}
		}
	}
}

func chunkValues(t *testing.T, policy string) []any {
	t.Helper()
	var parsed struct {
		SiteID []any `json:"SiteID"`
	}
	if err := json.Unmarshal([]byte(policy), &parsed); err != nil {
		t.Fatalf("policy is not the expected shape: %v\n%s", err, policy)
	}
	return parsed.SiteID
}

func chunkMatches(values []any, site string) bool {
	for _, v := range values {
		switch v := v.(type) {
		case string:
			if v == site {
				return true
			}
		case map[string]any:
			if p, ok := v["prefix"].(string); ok && strings.HasPrefix(site, p) {
				return true
			}
		}
	}
	return false
}

// The chunk policy had no ceiling at all. Past 150 sites the write failed and
// the filter kept its old list, so stations subscribed after that never
// received a chunk.
func TestChunkFilterPolicyMatchesEverySubscribedSite(t *testing.T) {
	t.Parallel()
	all := nexrad.Stations()
	for _, n := range []int{1, 150, 151, len(all)} {
		subscribed := all[len(all)-n:]
		policy, collapsed, err := chunkFilterPolicy(subscribed)
		if err != nil {
			t.Fatalf("%d sites: %v", n, err)
		}
		values := chunkValues(t, policy)
		if len(values) > maxChunkFilterValues {
			t.Errorf("%d sites rendered %d values, over the %d SNS accepts",
				n, len(values), maxChunkFilterValues)
		}
		if collapsed != (n > maxChunkFilterValues) {
			t.Errorf("%d sites: collapsed = %v", n, collapsed)
		}
		for _, site := range subscribed {
			if !chunkMatches(values, site) {
				t.Errorf("%d sites: %s is not matched by %s", n, site, policy)
			}
		}
	}
}

// Junk from a websocket path used to go straight into the filter policies.
func TestListenRejectsUnknownStations(t *testing.T) {
	t.Parallel()
	l := &Listener{archiveSites: sites(), chunkSites: sites()}
	for _, station := range []string{"nonsense", "KZZZ", "", "KTLX/"} {
		if err := l.ListenChunk(t.Context(), station); !errors.Is(err, nexrad.ErrUnknownStation) {
			t.Errorf("ListenChunk(%q) = %v, want ErrUnknownStation", station, err)
		}
		if err := l.ListenArchive(t.Context(), station); !errors.Is(err, nexrad.ErrUnknownStation) {
			t.Errorf("ListenArchive(%q) = %v, want ErrUnknownStation", station, err)
		}
	}
	if got := subscribedSites(l.chunkSites); len(got) != 0 {
		t.Errorf("rejected stations reached the chunk filter: %v", got)
	}
	if got := subscribedSites(l.archiveSites); len(got) != 0 {
		t.Errorf("rejected stations reached the archive filter: %v", got)
	}
}

// Both days are given up before any site is: a stale date costs latency until
// the next refresh, a dropped site costs every notification.
func TestArchiveFilterPolicyGivesUpTheSecondDayBeforeASite(t *testing.T) {
	t.Parallel()
	sites := make([]string, 0, 30)
	for i := range 30 {
		sites = append(sites, fmt.Sprintf("K%03d", i))
	}
	// 18 sites x 2 days = 36, exactly what fits.
	policy, collapsed, err := archiveFilterPolicy(sites[:18], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if collapsed || strings.Count(policy, `{"prefix":`) != 36 {
		t.Errorf("18 sites should fit as 36 values, got %d values, collapsed %v",
			strings.Count(policy, `{"prefix":`), collapsed)
	}
	// 19 would be 38, so the second day goes rather than a site.
	policy, collapsed, err = archiveFilterPolicy(sites[:19], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if collapsed {
		t.Error("19 sites should still all be named individually")
	}
	if got := strings.Count(policy, `{"prefix":`); got != 19 {
		t.Errorf("19 sites should fall back to one day each, got %d values", got)
	}
}

func TestPollBackoffRampsAndCaps(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		failures int
		want     time.Duration
	}{
		{0, pollRetryBase},
		{1, pollRetryBase},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second}, // the last rung under the ceiling
		{6, pollRetryMax},
		{100, pollRetryMax},
	} {
		if got := pollBackoff(tc.failures); got != tc.want {
			t.Errorf("pollBackoff(%d) = %s, want %s", tc.failures, got, tc.want)
		}
	}
	// The point of the ramp is that it is never zero, or the loop spins.
	for f := 0; f < 200; f++ {
		if d := pollBackoff(f); d <= 0 || d > pollRetryMax {
			t.Fatalf("pollBackoff(%d) = %s, outside (0, %s]", f, d, pollRetryMax)
		}
	}
}

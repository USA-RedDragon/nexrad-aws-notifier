package sqs

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/puzpuzpuz/xsync/v3"
)

func sites() *xsync.MapOf[string, uint] {
	return xsync.NewMapOf[string, uint]()
}

func TestListenRefcountsPerStation(t *testing.T) {
	t.Parallel()
	m := sites()

	listen(m, "KTLX")
	listen(m, "KTLX")
	if got := subscribedSites(m); len(got) != 1 || got[0] != "KTLX" {
		t.Fatalf("two listens should name the site once, got %v", got)
	}

	unlisten(m, "KTLX")
	if got := subscribedSites(m); len(got) != 1 {
		t.Fatalf("one of two listeners left, site should still be subscribed, got %v", got)
	}

	unlisten(m, "KTLX")
	if got := subscribedSites(m); len(got) != 0 {
		t.Fatalf("last listener gone, site should be unsubscribed, got %v", got)
	}
	if _, ok := m.Load("KTLX"); ok {
		t.Fatal("last listener gone, entry should be deleted rather than left at zero")
	}
}

// Decrementing a station nobody is listening to used to underflow uint to
// 1<<64-1, leaving an entry that subscribedSites would then treat as live.
func TestUnlistenUnknownStationDoesNotUnderflow(t *testing.T) {
	t.Parallel()
	m := sites()

	unlisten(m, "KTLX")

	if v, ok := m.Load("KTLX"); ok {
		t.Fatalf("unlisten of an absent station left an entry: %d", v)
	}
	if got := subscribedSites(m); len(got) != 0 {
		t.Fatalf("unlisten of an absent station subscribed it: %v", got)
	}

	// And the entry it used to leave behind must not resurrect the site on a
	// later decrement either.
	unlisten(m, "KTLX")
	if got := subscribedSites(m); len(got) != 0 {
		t.Fatalf("second unlisten subscribed the station: %v", got)
	}
}

func TestSubscribedSitesIsSortedAndSkipsZero(t *testing.T) {
	t.Parallel()
	m := sites()
	listen(m, "KTLX")
	listen(m, "KABC")
	listen(m, "KOUN")
	m.Store("KZZZ", 0)

	got := subscribedSites(m)
	want := []string{"KABC", "KOUN", "KTLX"}
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
		{"one site", []string{"KTLX"}, `{"SiteID": ["KTLX"]}`},
		{"many sites", []string{"KABC", "KTLX"}, `{"SiteID": ["KABC","KTLX"]}`},
	} {
		got, err := chunkFilterPolicy(tc.sites)
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

	got, dropped, err := archiveFilterPolicy([]string{"KTLX"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 0 {
		t.Fatalf("one site should fit, dropped %d", dropped)
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
		[]string{"KTLX"}, time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC))
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
// filter keeps whatever it had. Measured against live SNS -- 36 values
// accepted, 40 refused as "Filter policy is too complex".
func TestArchiveFilterPolicyStaysInsideWhatSNSAccepts(t *testing.T) {
	t.Parallel()
	sites := make([]string, 0, 200)
	for i := range 200 {
		sites = append(sites, fmt.Sprintf("K%03d", i))
	}
	for _, n := range []int{1, 6, 17, 18, 19, 36, 37, 100, 200} {
		policy, dropped, err := archiveFilterPolicy(sites[:n], time.Now())
		if err != nil {
			t.Fatalf("%d sites: %v", n, err)
		}
		values := strings.Count(policy, `{"prefix":`)
		if values > maxArchiveFilterValues {
			t.Errorf("%d sites rendered %d values, over the %d SNS accepts",
				n, values, maxArchiveFilterValues)
		}
		if n <= maxArchiveFilterValues && dropped != 0 {
			t.Errorf("%d sites fit in %d values but %d were dropped", n, values, dropped)
		}
		if n > maxArchiveFilterValues && dropped != n-maxArchiveFilterValues {
			t.Errorf("%d sites: dropped %d, want %d", n, dropped, n-maxArchiveFilterValues)
		}
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
	policy, dropped, err := archiveFilterPolicy(sites[:18], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 0 || strings.Count(policy, `{"prefix":`) != 36 {
		t.Errorf("18 sites should fit as 36 values, got %d values and %d dropped",
			strings.Count(policy, `{"prefix":`), dropped)
	}
	// 19 would be 38, so the second day goes rather than a site.
	policy, dropped, err = archiveFilterPolicy(sites[:19], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 0 {
		t.Errorf("19 sites should still all be named, dropped %d", dropped)
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

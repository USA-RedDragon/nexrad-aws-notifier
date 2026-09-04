package sqs

import (
	"strings"
	"testing"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
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
// policy is matched against the body. The exact shape below is the one verified
// against live SNS: `Records` written as though it were an object, and a
// wildcard rather than a prefix because the key begins with the date.
func TestArchiveFilterPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		sites []string
		want  string
	}{
		{
			"no sites matches nothing",
			nil,
			`{"Records":{"eventName":[{"prefix":"ObjectCreated:"}],"s3":{"object":{"key":[{"wildcard":"*/nonsense/*"}]}}}}`,
		},
		{
			"one site",
			[]string{"KTLX"},
			`{"Records":{"eventName":[{"prefix":"ObjectCreated:"}],"s3":{"object":{"key":[{"wildcard":"*/KTLX/*"}]}}}}`,
		},
		{
			"many sites",
			[]string{"KABC", "KTLX"},
			`{"Records":{"eventName":[{"prefix":"ObjectCreated:"}],"s3":{"object":{"key":[{"wildcard":"*/KABC/*"},{"wildcard":"*/KTLX/*"}]}}}}`,
		},
	} {
		got, err := archiveFilterPolicy(tc.sites)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

// A site must never be spelled into the policy as a bare prefix: the object key
// is `YYYY/MM/DD/SITE/...`, so a prefix on the site matches nothing at all.
func TestArchiveFilterPolicyDoesNotPrefixTheSite(t *testing.T) {
	t.Parallel()
	got, err := archiveFilterPolicy([]string{"KTLX"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"wildcard":"*/KTLX/*"}`; !strings.Contains(got, want) {
		t.Fatalf("policy should match the site by wildcard, got %s", got)
	}
	if bad := `{"prefix":"KTLX`; strings.Contains(got, bad) {
		t.Fatalf("policy prefixes the site, which can never match: %s", got)
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

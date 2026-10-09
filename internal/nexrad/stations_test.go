package nexrad_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/nexrad"
)

func TestStationAcceptsRealSites(t *testing.T) {
	t.Parallel()
	for _, id := range []string{
		"KTLX", "KOUN", "PHKI", "TJUA", "PGUA", "RKSG", "LPLA", // WSR-88D
		"TBOS", "TJFK", "TSJU", // TDWR
		"ktlx", " KTLX ", // normalized
	} {
		got, err := nexrad.Station(id)
		if err != nil {
			t.Errorf("Station(%q): %v", id, err)
			continue
		}
		if want := strings.ToUpper(strings.TrimSpace(id)); got != want {
			t.Errorf("Station(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestStationRejectsJunk(t *testing.T) {
	t.Parallel()
	for _, id := range []string{
		"", "K", "KTL", "KTLXX", "KZZZ", "nonsense", "KTLX/", "../KTLX",
		"KT X", "*", `"KTLX"`, "KTLX\x00", "ＫＴＬＸ",
	} {
		if got, err := nexrad.Station(id); !errors.Is(err, nexrad.ErrUnknownStation) {
			t.Errorf("Station(%q) = %q, %v; want ErrUnknownStation", id, got, err)
		}
	}
}

func TestStationsIsSortedAndUnique(t *testing.T) {
	t.Parallel()
	got := nexrad.Stations()
	if !slices.IsSorted(got) {
		t.Error("Stations() is not sorted")
	}
	if len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Error("Stations() has duplicates")
	}
	// Every operational WSR-88D and TDWR, plus the ROC test beds.
	if len(got) < 200 {
		t.Errorf("Stations() has %d entries, want the full site list", len(got))
	}
	for _, id := range got {
		if _, err := nexrad.Station(id); err != nil {
			t.Errorf("listed station %q is rejected: %v", id, err)
		}
	}
	// Callers get a copy and may not edit the list underneath Station.
	got[0] = "XXXX"
	if _, err := nexrad.Station("XXXX"); err == nil {
		t.Error("editing the returned slice changed the accepted set")
	}
}

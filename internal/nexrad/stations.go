// Package nexrad knows which radar sites publish to the NEXRAD Level II
// buckets, so a subscription can be checked before it reaches an SNS filter.
package nexrad

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrUnknownStation is returned for an ID that is not a NEXRAD site.
var ErrUnknownStation = errors.New("unknown NEXRAD station")

// stations lists every site ID the unidata-nexrad-level2 and
// unidata-nexrad-level2-chunks buckets key their objects by: each WSR-88D and
// TDWR in NCEI's station list
// (https://www.ncei.noaa.gov/access/homr/file/nexrad-stations.txt), plus the
// Radar Operations Center test and engineering IDs that also appear in the
// buckets. Kept sorted for Stations and binary search.
//
//nolint:gochecknoglobals // a constant table; Go has no constant slices
var stations = []string{
	"DAN1", "DOP1", "FOP1", "KABR", "KABX", "KAKQ", "KAMA", "KAMX", "KAPX", "KARX",
	"KATX", "KBBX", "KBGM", "KBHX", "KBIS", "KBLX", "KBMX", "KBOX", "KBRO", "KBUF",
	"KBYX", "KCAE", "KCBW", "KCBX", "KCCX", "KCLE", "KCLX", "KCRI", "KCRP", "KCXX",
	"KCYS", "KDAX", "KDDC", "KDFX", "KDGX", "KDIX", "KDLH", "KDMX", "KDOX", "KDTX",
	"KDVN", "KDYX", "KEAX", "KEMX", "KENX", "KEOX", "KEPZ", "KESX", "KEVX", "KEWX",
	"KEYX", "KFCX", "KFDR", "KFDX", "KFFC", "KFSD", "KFSX", "KFTG", "KFWS", "KGGW",
	"KGJX", "KGLD", "KGRB", "KGRK", "KGRR", "KGSP", "KGWX", "KGYX", "KHDC", "KHDX",
	"KHGX", "KHNX", "KHPX", "KHTX", "KICT", "KICX", "KILN", "KILX", "KIND", "KINX",
	"KIWA", "KIWX", "KJAX", "KJGX", "KJKL", "KLBB", "KLCH", "KLGX", "KLIX", "KLNX",
	"KLOT", "KLRX", "KLSX", "KLTX", "KLVX", "KLWX", "KLZK", "KMAF", "KMAX", "KMBX",
	"KMHX", "KMKX", "KMLB", "KMOB", "KMPX", "KMQT", "KMRX", "KMSX", "KMTX", "KMUX",
	"KMVX", "KMXX", "KNKX", "KNQA", "KOAX", "KOHX", "KOKX", "KOTX", "KOUN", "KPAH",
	"KPBZ", "KPDT", "KPOE", "KPUX", "KRAX", "KRGX", "KRIW", "KRLX", "KRTX", "KSFX",
	"KSGF", "KSHV", "KSJT", "KSOX", "KSRX", "KTBW", "KTFX", "KTLH", "KTLX", "KTWX",
	"KTYX", "KUDX", "KUEX", "KVAX", "KVBX", "KVNX", "KVTX", "KVWX", "KYUX", "LPLA",
	"NOP3", "NOP4", "PABC", "PACG", "PAEC", "PAHG", "PAIH", "PAKC", "PAPD", "PGUA",
	"PHKI", "PHKM", "PHMO", "PHWA", "RKJK", "RKSG", "RODN", "ROK1", "ROP3", "ROP4",
	"TADW", "TATL", "TBNA", "TBOS", "TBWI", "TCLT", "TCMH", "TCVG", "TDAL", "TDAY",
	"TDCA", "TDEN", "TDFW", "TDJT", "TDTW", "TEWR", "TFLL", "THOU", "TIAD", "TIAH",
	"TICH", "TIDS", "TJBQ", "TJFK", "TJRV", "TJUA", "TLAS", "TLVE", "TMCI", "TMCO",
	"TMDW", "TMEM", "TMIA", "TMKE", "TMSP", "TMSY", "TOK2", "TOKC", "TORD", "TPBI",
	"TPHL", "TPHX", "TPIT", "TRDU", "TSDF", "TSJU", "TSLC", "TSTL", "TTPA", "TTUL",
}

// Station normalizes id to the upper-case four-letter form the buckets use, or
// returns ErrUnknownStation if it does not name a NEXRAD site.
func Station(id string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(id))
	if _, found := slices.BinarySearch(stations, normalized); !found {
		return "", fmt.Errorf("%w %q: expected a four-letter site ID such as KTLX", ErrUnknownStation, id)
	}
	return normalized, nil
}

// Stations returns a sorted copy of every accepted site ID.
func Stations() []string {
	return slices.Clone(stations)
}

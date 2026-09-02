// Command generate_turkish_address_seed converts the PTT-derived nested
// address snapshot into a deterministic PostgreSQL migration.
//
// Neighborhood identifiers in the source are opaque strings. The application
// API intentionally keeps bigint identifiers, so this generator derives a
// positive 63-bit identifier from each source key and fails on collisions.
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type province struct {
	ID        string     `json:"il_id"`
	Districts []district `json:"ilceler"`
}

type district struct {
	ID            string         `json:"ilce_id"`
	Name          string         `json:"ilce_adi"`
	Neighborhoods []neighborhood `json:"mahalleler"`
}

type neighborhood struct {
	ID   string `json:"mahalle_id"`
	Name string `json:"mahalle_adi"`
}

type districtRow struct {
	ID         int64
	ProvinceID int64
	Name       string
}

type neighborhoodRow struct {
	ID         int64
	DistrictID int64
	Name       string
}

func main() {
	inputPath := flag.String("input", "", "nested PTT snapshot JSON")
	outputPath := flag.String("output", "", "SQL migration output")
	snapshotDate := flag.String("snapshot", "unknown", "source snapshot date")
	flag.Parse()
	if *inputPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "usage: generate_turkish_address_seed -input snapshot.json -output migration.sql -snapshot YYYY-MM-DD")
		os.Exit(2)
	}

	input, err := os.ReadFile(*inputPath)
	if err != nil {
		fatal(err)
	}
	var provinces []province
	if err := json.Unmarshal(input, &provinces); err != nil {
		fatal(err)
	}

	districtRows := make([]districtRow, 0, 973)
	neighborhoodRows := make([]neighborhoodRow, 0, 72984)
	seenDistrictIDs := map[int64]struct{}{}
	seenNeighborhoodIDs := map[int64]string{}
	seenProvinceIDs := map[int64]struct{}{}
	for _, item := range provinces {
		provinceID := parsePositive(item.ID, "province")
		seenProvinceIDs[provinceID] = struct{}{}
		for _, district := range item.Districts {
			districtID := parsePositive(district.ID, "district")
			if _, exists := seenDistrictIDs[districtID]; exists {
				fatalf("duplicate district id %d", districtID)
			}
			seenDistrictIDs[districtID] = struct{}{}
			districtRows = append(districtRows, districtRow{ID: districtID, ProvinceID: provinceID, Name: district.Name})
			for _, neighborhood := range district.Neighborhoods {
				if neighborhood.ID == "" || strings.TrimSpace(neighborhood.Name) == "" {
					fatalf("neighborhood has an empty id or name in district %d", districtID)
				}
				id := stableID(neighborhood.ID)
				if previous, exists := seenNeighborhoodIDs[id]; exists && previous != neighborhood.ID {
					fatalf("neighborhood id hash collision %d: %q and %q", id, previous, neighborhood.ID)
				}
				seenNeighborhoodIDs[id] = neighborhood.ID
				neighborhoodRows = append(neighborhoodRows, neighborhoodRow{ID: id, DistrictID: districtID, Name: neighborhood.Name})
			}
		}
	}
	if len(provinces) != 81 || len(seenProvinceIDs) != 81 || len(districtRows) != 973 {
		fatalf("unexpected source dimensions: provinces=%d unique provinces=%d districts=%d", len(provinces), len(seenProvinceIDs), len(districtRows))
	}

	sort.Slice(districtRows, func(i, j int) bool {
		if districtRows[i].ProvinceID != districtRows[j].ProvinceID {
			return districtRows[i].ProvinceID < districtRows[j].ProvinceID
		}
		return districtRows[i].ID < districtRows[j].ID
	})
	sort.Slice(neighborhoodRows, func(i, j int) bool {
		if neighborhoodRows[i].DistrictID != neighborhoodRows[j].DistrictID {
			return neighborhoodRows[i].DistrictID < neighborhoodRows[j].DistrictID
		}
		// Compare on the folded name once so the "differ?" test and the ordering
		// stay consistent; fall back to the stable ID for exact-fold ties.
		ni, nj := strings.ToLower(neighborhoodRows[i].Name), strings.ToLower(neighborhoodRows[j].Name)
		if ni != nj {
			return ni < nj
		}
		return neighborhoodRows[i].ID < neighborhoodRows[j].ID
	})

	output, err := os.Create(*outputPath)
	if err != nil {
		fatal(err)
	}
	defer output.Close()
	fmt.Fprintf(output, `-- PTT-derived Turkey address snapshot.
-- Source: https://github.com/cyaxaress/turkiye-il-ilce-mah
-- Snapshot: %s; rows: %d districts, %d neighborhoods.
-- Neighborhood source keys are converted to deterministic positive bigint IDs.
-- The source contains repeated neighborhood names within a district, so the
-- historical district/name uniqueness constraint is intentionally removed.

ALTER TABLE turkish_neighborhoods
    DROP CONSTRAINT IF EXISTS turkish_neighborhoods_district_id_name_key;

`, *snapshotDate, len(districtRows), len(neighborhoodRows))
	writeDistricts(output, districtRows)
	writeNeighborhoods(output, neighborhoodRows)
}

func writeDistricts(output *os.File, rows []districtRow) {
	writeBatches(output, len(rows), "turkish_districts", "province_id", func(index int) string {
		row := rows[index]
		return fmt.Sprintf("    (%d, %d, %s)", row.ID, row.ProvinceID, quote(row.Name))
	})
}

func writeNeighborhoods(output *os.File, rows []neighborhoodRow) {
	writeBatches(output, len(rows), "turkish_neighborhoods", "district_id", func(index int) string {
		row := rows[index]
		return fmt.Sprintf("    (%d, %d, %s)", row.ID, row.DistrictID, quote(row.Name))
	})
}

func writeBatches(output *os.File, count int, table, parentColumn string, row func(int) string) {
	const batchSize = 1000
	for start := 0; start < count; start += batchSize {
		fmt.Fprintf(output, "INSERT INTO %s (id, %s, name) VALUES\n", table, parentColumn)
		end := start + batchSize
		if end > count {
			end = count
		}
		for index := start; index < end; index++ {
			separator := ","
			if index == end-1 {
				separator = ";"
			}
			fmt.Fprintln(output, row(index)+separator)
		}
		if end < count {
			fmt.Fprintln(output)
		}
	}
}

func parsePositive(value, kind string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		fatalf("invalid %s id %q", kind, value)
	}
	return parsed
}

func stableID(sourceID string) int64 {
	digest := sha256.Sum256([]byte(sourceID))
	id := int64(binary.BigEndian.Uint64(digest[:8]) & 0x7fffffffffffffff)
	if id == 0 {
		return 1
	}
	return id
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func fatal(err error) {
	fatalf("%v", err)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

package marsgeo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Pinned client contract, commit 147863f (testdata/contract/README.md).
var contractFileSHA256 = map[string]string{
	"mars_terrain_data.v1.schema.json": "1e162cd1f69d3aedc1a26303fbaf839a2d14d52ce25c84ad7d8fb065e4451b34",
	"synthetic_2x2.v1.json":            "4ae24d860b9a009f162ae94d48adbed36ddc9ea3781d7921f47a046278e300f5",
	"synthetic_50x50.v1.json":          "b4053e965f62e301926503c65cc314dca014a7e4412a4bbe7e199214c59a03ba",
}

func readContractFile(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "contract", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var (
	schemaOnce     sync.Once
	compiledSchema *jsonschema.Schema
	schemaErr      error
)

// validatePayloadBytes is the contract §8 validator: the real schema plus the
// two cross-field rules, applied to the bytes exactly as they would be sent.
func validatePayloadBytes(t testing.TB, payload []byte) error {
	t.Helper()
	schemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(readContractFile(t, "mars_terrain_data.v1.schema.json")))
		if err != nil {
			schemaErr = err
			return
		}
		const url = "urn:space-chonkers:mars-terrain-data:v1"
		c := jsonschema.NewCompiler()
		if err := c.AddResource(url, doc); err != nil {
			schemaErr = err
			return
		}
		compiledSchema, schemaErr = c.Compile(url)
	})
	if schemaErr != nil {
		t.Fatalf("compile schema: %v", schemaErr)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("payload is not JSON: %w", err)
	}
	if err := compiledSchema.Validate(inst); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	var d MarsTerrainDataV1
	if err := json.Unmarshal(payload, &d); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return CheckShape(&d)
}

// mustValidPayload encodes d and checks the encoded bytes.
func mustValidPayload(t testing.TB, d *MarsTerrainDataV1) []byte {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := validatePayloadBytes(t, b); err != nil {
		t.Fatalf("payload invalid: %v", err)
	}
	return b
}

func TestContractFilesArePinned(t *testing.T) {
	for name, want := range contractFileSHA256 {
		sum := sha256.Sum256(readContractFile(t, name))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s sha256 = %s, want %s (client commit 147863f)", name, got, want)
		}
	}
}

func TestContractFixturesValidate(t *testing.T) {
	for _, name := range []string{"synthetic_2x2.v1.json", "synthetic_50x50.v1.json"} {
		if err := validatePayloadBytes(t, readContractFile(t, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func validTestPayload() MarsTerrainDataV1 {
	samples := make([]float64, 9)
	for i := range samples {
		samples[i] = -4000 + float64(i)
	}
	return MarsTerrainDataV1{
		Version:   ContractVersion,
		Center:    Coordinate{Latitude: 10, LongitudeEast: 20},
		Grid:      Grid{Columns: 2, Rows: 2, CellSizeMeters: 10},
		Elevation: ElevationSamples{Sampling: SamplingCellCorners, Columns: 3, Rows: 3, SamplesMeters: samples, Datum: Datum},
		Source:    Source{DatasetID: "d", Provider: "p", ResolutionMeters: 1},
		Seed:      4294967295,
	}
}

func TestCheckShapeRejects(t *testing.T) {
	cases := map[string]func(d *MarsTerrainDataV1){
		"elevation columns != grid+1": func(d *MarsTerrainDataV1) { d.Elevation.Columns = 2 },
		"elevation rows != grid+1":    func(d *MarsTerrainDataV1) { d.Elevation.Rows = 4 },
		"sample count":                func(d *MarsTerrainDataV1) { d.Elevation.SamplesMeters = d.Elevation.SamplesMeters[:8] },
		"NaN sample":                  func(d *MarsTerrainDataV1) { d.Elevation.SamplesMeters[4] = math.NaN() },
		"+Inf sample":                 func(d *MarsTerrainDataV1) { d.Elevation.SamplesMeters[0] = math.Inf(1) },
		"MISSING_CONSTANT sample": func(d *MarsTerrainDataV1) {
			d.Elevation.SamplesMeters[8] = float64(math.Float32frombits(0xFF7FFFFB))
		},
		"ISIS HRS4 sample": func(d *MarsTerrainDataV1) {
			d.Elevation.SamplesMeters[1] = float64(math.Float32frombits(0xFF7FFFFF))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := validTestPayload()
			mutate(&d)
			if err := CheckShape(&d); err == nil {
				t.Fatal("CheckShape accepted an invalid payload")
			}
		})
	}
	d := validTestPayload()
	if err := CheckShape(&d); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
}

func TestPayloadEncoding(t *testing.T) {
	d := validTestPayload()
	b := mustValidPayload(t, &d) // seed 4294967295 accepted
	if strings.Contains(string(b), "productId") {
		t.Fatalf("nil productId must be omitted, got %s", b)
	}
	id := "P"
	d.Source.ProductID = &id
	if b = mustValidPayload(t, &d); !strings.Contains(string(b), `"productId":"P"`) {
		t.Fatalf("productId not encoded: %s", b)
	}
	d.Elevation.SamplesMeters[0] = math.NaN()
	if _, err := json.Marshal(&d); err == nil {
		t.Fatal("json.Marshal must refuse NaN")
	}
}

func TestSchemaRejectsContractViolations(t *testing.T) {
	base := validTestPayload()
	raw, _ := json.Marshal(&base)
	cases := map[string][2]string{
		"null productId": {`"resolutionMeters":1}`, `"resolutionMeters":1,"productId":null}`},
		"latitude > 85":  {`"latitude":10`, `"latitude":85.5`},
		"seed >= 2^32":   {`"seed":4294967295`, `"seed":4294967296`},
		"blank datum":    {`"datum":"mars-2000-areoid"`, `"datum":" "`},
	}
	for name, r := range cases {
		mutated := strings.Replace(string(raw), r[0], r[1], 1)
		if mutated == string(raw) {
			t.Fatalf("%s: replacement did not apply", name)
		}
		if err := validatePayloadBytes(t, []byte(mutated)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

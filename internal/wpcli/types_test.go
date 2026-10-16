package wpcli

import (
	"encoding/json"
	"strings"
	"testing"
)

// Shaped like live `wp plugin list --format=json` output: must-use and dropin
// rows report "update": false (fleet#255).
const pluginListWithBoolUpdate = `[
{"name":"akismet","status":"active","update":"none","version":"5.3","update_version":"","auto_update":"on"},
{"name":"hello-dolly","status":"inactive","update":"available","version":"1.7.2","update_version":"1.7.3","auto_update":"off"},
{"name":"mu-loader","status":"must-use","update":false,"version":"1.0","update_version":"","auto_update":"off"},
{"name":"wpe-cache","status":"must-use","update":false,"version":"2.1","update_version":"","auto_update":"off"},
{"name":"advanced-cache.php","status":"dropin","update":false,"version":"","update_version":"","auto_update":"off"},
{"name":"maintenance.php","status":"dropin","update":false,"version":"","update_version":"","auto_update":"off"},
{"name":"bool-true","status":"active","update":true,"version":"3.0","update_version":"3.1","auto_update":"off"},
{"name":"null-update","status":"inactive","update":null,"version":"0.9","update_version":"","auto_update":"off"}
]`

func TestParseJSONPluginsDecodesBoolAndNullUpdate(t *testing.T) {
	plugins, err := ParseJSON[Plugin](pluginListWithBoolUpdate)
	if err != nil {
		t.Fatalf("ParseJSON error: %v", err)
	}
	want := []Plugin{
		{Name: "akismet", Status: "active", Update: "none", Version: "5.3", AutoUpdate: "on"},
		{Name: "hello-dolly", Status: "inactive", Update: "available", Version: "1.7.2", UpdateVersion: "1.7.3", AutoUpdate: "off"},
		{Name: "mu-loader", Status: "must-use", Update: "none", Version: "1.0", AutoUpdate: "off"},
		{Name: "wpe-cache", Status: "must-use", Update: "none", Version: "2.1", AutoUpdate: "off"},
		{Name: "advanced-cache.php", Status: "dropin", Update: "none", Version: "", AutoUpdate: "off"},
		{Name: "maintenance.php", Status: "dropin", Update: "none", Version: "", AutoUpdate: "off"},
		{Name: "bool-true", Status: "active", Update: "available", Version: "3.0", UpdateVersion: "3.1", AutoUpdate: "off"},
		{Name: "null-update", Status: "inactive", Update: "", Version: "0.9", AutoUpdate: "off"},
	}
	if len(plugins) != len(want) {
		t.Fatalf("got %d plugins, want %d", len(plugins), len(want))
	}
	for i := range want {
		if plugins[i] != want[i] {
			t.Errorf("plugins[%d] = %+v, want %+v", i, plugins[i], want[i])
		}
	}
}

func TestParseJSONThemesDecodesBoolUpdate(t *testing.T) {
	input := `[{"name":"twentytwentyfour","status":"active","update":false,"version":"1.2","auto_update":"off"},{"name":"astra","status":"inactive","update":"available","version":"4.6","auto_update":"on"}]`

	themes, err := ParseJSON[Theme](input)
	if err != nil {
		t.Fatalf("ParseJSON error: %v", err)
	}
	want := []Theme{
		{Name: "twentytwentyfour", Status: "active", Update: "none", Version: "1.2", AutoUpdate: "off"},
		{Name: "astra", Status: "inactive", Update: "available", Version: "4.6", AutoUpdate: "on"},
	}
	if len(themes) != len(want) {
		t.Fatalf("got %d themes, want %d", len(themes), len(want))
	}
	for i := range want {
		if themes[i] != want[i] {
			t.Errorf("themes[%d] = %+v, want %+v", i, themes[i], want[i])
		}
	}
}

func TestParseSinglePluginDecodesBoolUpdate(t *testing.T) {
	input := `{"name":"advanced-cache.php","status":"dropin","update":false,"version":"","auto_update":"off"}`

	p, err := ParseSingle[Plugin](input)
	if err != nil {
		t.Fatalf("ParseSingle error: %v", err)
	}
	want := Plugin{Name: "advanced-cache.php", Status: "dropin", Update: "none", AutoUpdate: "off"}
	if *p != want {
		t.Errorf("got %+v, want %+v", *p, want)
	}
}

func TestPluginUpdateMarshalsAsString(t *testing.T) {
	plugins, err := ParseJSON[Plugin](pluginListWithBoolUpdate)
	if err != nil {
		t.Fatalf("ParseJSON error: %v", err)
	}
	out, err := json.Marshal(plugins)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	want := []string{"none", "available", "none", "none", "none", "none", "available", ""}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		got, ok := rows[i]["update"].(string)
		if !ok {
			t.Errorf("rows[%d].update = %#v (%T), want string %q", i, rows[i]["update"], rows[i]["update"], w)
			continue
		}
		if got != w {
			t.Errorf("rows[%d].update = %q, want %q", i, got, w)
		}
	}
}

func TestParseJSONPluginsRejectsNonStringUpdate(t *testing.T) {
	tests := []struct {
		name   string
		update string
	}{
		{"number", `1`},
		{"object", `{"version":"1.0"}`},
		{"array", `["none"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `[{"name":"x","status":"active","update":` + tt.update + `,"version":"1.0","auto_update":"off"}]`
			_, err := ParseJSON[Plugin](input)
			if err == nil {
				t.Fatalf("ParseJSON accepted update=%s, want error", tt.update)
			}
			if !strings.Contains(err.Error(), "want string, bool, or null") {
				t.Errorf("error = %v, want FlexString rejection", err)
			}
		})
	}
}

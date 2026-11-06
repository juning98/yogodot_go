package api_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wednesque/yogod/internal/api"
	"github.com/wednesque/yogod/internal/store"
)

func TestInvalidSettingsRequestPublishesNothing(t *testing.T) {
	h := newHarness(t)
	before := h.set.Snapshot()
	rec := h.do(http.MethodPost, "/api/settings", map[string]any{"calibration": map[string]any{"gain": []float64{1, 1, 1}, "corr": .5, "sat": 1, "eql": 255}, "rate_tier": 9})
	if rec.Code != 400 || h.set.Snapshot().Calib != before.Calib {
		t.Fatal("partially applied invalid settings")
	}
	_, _, saved := h.calls()
	if saved != 0 {
		t.Fatal("invalid request saved")
	}
}
func TestFailedSaveDoesNotApplyOrReportSuccess(t *testing.T) {
	for _, path := range []string{"/api/brightness", "/api/theme", "/api/settings"} {
		t.Run(path, func(t *testing.T) {
			st := store.Defaults()
			before := st.Snapshot()
			applied := false
			s := api.New(api.Deps{Settings: &st, Save: func() error { return errors.New("disk full") }, ApplyOutput: func() { applied = true }, SwitchTheme: func(string, bool) error { applied = true; return nil }})
			body := map[string]string{"/api/brightness": `{"dot":10}`, "/api/theme": `{"id":"panda"}`, "/api/settings": `{"outputs":{"dot":false,"keys":true}}`}[path]
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
			after := st.Snapshot()
			if rec.Code < 400 || !strings.Contains(rec.Body.String(), "disk full") {
				t.Fatalf("failure hidden: %d %s", rec.Code, rec.Body.String())
			}
			if applied || after.Bright != before.Bright || after.Theme != before.Theme || after.Outputs != before.Outputs {
				t.Fatal("failed save published output")
			}
		})
	}
}

func TestRetiredStatisticsAreAbsent(t *testing.T) {
	h := newHarness(t)
	dev := h.state()["device"].(map[string]any)
	for _, key := range []string{"sent", "dropped", "dropBy"} {
		if _, ok := dev[key]; ok {
			t.Fatalf("retired field remains: %s", key)
		}
	}
}

func TestOutputSwitchOnlyChangesRequestedChannel(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/api/settings", map[string]any{"outputs": map[string]bool{"dot": false}})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if c := h.set.Snapshot().Outputs; c.Dot || !c.Keys {
		t.Fatalf("dot switch changed keys: %+v", c)
	}
	rec = h.do(http.MethodPost, "/api/settings", map[string]any{"outputs": map[string]bool{"keys": false}})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = h.do(http.MethodPost, "/api/settings", map[string]any{"outputs": map[string]bool{"dot": true}})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if c := h.set.Snapshot().Outputs; !c.Dot || c.Keys {
		t.Fatalf("dot resume changed keys: %+v", c)
	}
}

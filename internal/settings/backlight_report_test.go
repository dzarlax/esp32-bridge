package settings

import "testing"

func lightInt(v int) *int        { return &v }
func lightTime(v uint64) *uint64 { return &v }
func previewReport() *BacklightReport {
	return &BacklightReport{Version: 1, Phase: "preview", LastCommand: lightInt(70), OutputKnown: true,
		TestCommand: lightInt(70), ReturnCommand: lightInt(50), SampleUptimeMS: 12000, RestoreDeadlineUptimeMS: lightTime(20000)}
}
func TestBacklightReportValidation(t *testing.T) {
	if !validBacklightReport(nil, 0) {
		t.Fatal("legacy report rejected")
	}
	for name, mutate := range map[string]func(*BacklightReport){
		"version":                func(b *BacklightReport) { b.Version = 2 },
		"phase":                  func(b *BacklightReport) { b.Phase = "measured" },
		"bounds":                 func(b *BacklightReport) { b.LastCommand = lightInt(98) },
		"negative":               func(b *BacklightReport) { b.TestCommand = lightInt(-1) },
		"partial":                func(b *BacklightReport) { b.ReturnCommand = nil },
		"unknown-output":         func(b *BacklightReport) { b.OutputKnown = false },
		"future-sample":          func(b *BacklightReport) { b.SampleUptimeMS = 13000 },
		"unsafe-integer":         func(b *BacklightReport) { b.SampleUptimeMS = 1 << 53 },
		"deadline":               func(b *BacklightReport) { b.RestoreDeadlineUptimeMS = lightTime(999999) },
		"error":                  func(b *BacklightReport) { b.Error = "arbitrary text" },
		"wrong-restored-command": func(b *BacklightReport) { b.Phase = "restored" },
	} {
		t.Run(name, func(t *testing.T) {
			b := previewReport()
			mutate(b)
			if validBacklightReport(b, 12) {
				t.Fatal("invalid report accepted")
			}
		})
	}
	for _, b := range []*BacklightReport{
		{Version: 1, Phase: "unknown"},
		{Version: 1, Phase: "reference", LastCommand: lightInt(50), OutputKnown: true},
		{Version: 1, Phase: "error", LastCommand: lightInt(50), Error: "write_failed"},
		previewReport(),
	} {
		if !validBacklightReport(b, 12) {
			t.Fatalf("valid %s rejected", b.Phase)
		}
	}
	b := previewReport()
	b.SampleUptimeMS = 21000
	if !validBacklightReport(b, 21) {
		t.Fatal("overdue preview needs to remain pending, not invalid")
	}
	b.Phase = "restored"
	b.LastCommand = lightInt(50)
	if !validBacklightReport(b, 21) {
		t.Fatal("restoration rejected")
	}
}
func TestBacklightReportScopeOrderingAndReset(t *testing.T) {
	s, _ := newStore(t, nil)
	id, other := testID('a'), testID('b')
	d := report(s, id, "boot-a")
	d.UptimeSeconds = 12
	d.BacklightReport = previewReport()
	response, err := s.Sync(d)
	if err != nil || response.BacklightReportVersion != 1 {
		t.Fatalf("sync: %v", err)
	}
	register(t, s, other, "boot-b")
	view, _ := s.View(other, nil)
	if view.Device.BacklightReport != nil {
		t.Fatal("cross-device report leak")
	}
	d.BacklightReport.SampleUptimeMS = 11000
	if _, err = s.Sync(d); err == nil {
		t.Fatal("older snapshot accepted")
	}
	view, _ = s.View(id, nil)
	if view.Device.BacklightReport.SampleUptimeMS != 12000 {
		t.Fatal("older snapshot replaced state")
	}
	// Omitting the extension clears current state instead of inheriting history.
	d.BacklightReport = nil
	if _, err = s.Sync(d); err != nil {
		t.Fatal(err)
	}
	view, _ = s.View(id, nil)
	if view.Device.BacklightReport != nil {
		t.Fatal("missing report inherited old command")
	}
	d.BootID = "boot-new"
	d.UptimeSeconds = 0
	d.BacklightReport = &BacklightReport{Version: 1, Phase: "unknown"}
	if _, err = s.Sync(d); err != nil {
		t.Fatal(err)
	}
	view, _ = s.View(id, nil)
	if view.Device.BacklightReport.LastCommand != nil || view.Device.BacklightReport.OutputKnown {
		t.Fatal("old boot command retained")
	}
}

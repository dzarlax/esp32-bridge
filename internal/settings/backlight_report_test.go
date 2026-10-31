package settings

import (
	"fmt"
	"testing"
)

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

func TestBacklightStepSequenceAndEndpoints(t *testing.T) {
	s, _ := newStore(t, nil)
	d := report(s, testID('a'), "step-boot")
	for i, command := range []int{45, 40, 35, 1, 97} {
		sample := uint64(1000 + i*1000)
		d.UptimeSeconds = sample / 1000
		d.BacklightReport = &BacklightReport{Version: 1, Phase: "preview", LastCommand: lightInt(command), OutputKnown: true, TestCommand: lightInt(command), ReturnCommand: lightInt(50), SampleUptimeMS: sample, RestoreDeadlineUptimeMS: lightTime(sample + 20000)}
		if _, err := s.Sync(d); err != nil {
			t.Fatalf("step %d: %v", command, err)
		}
		v, _ := s.View(d.DeviceID, nil)
		if *v.Device.BacklightReport.LastCommand != command {
			t.Fatal("snapshot not updated")
		}
	}
	d.UptimeSeconds = 25
	d.BacklightReport.Phase = "restored"
	d.BacklightReport.LastCommand = lightInt(50)
	d.BacklightReport.SampleUptimeMS = 25000
	if _, err := s.Sync(d); err != nil {
		t.Fatal(err)
	}
	v, _ := s.View(d.DeviceID, nil)
	if *v.Device.BacklightReport.TestCommand != 97 || *v.Device.BacklightReport.LastCommand != 50 {
		t.Fatal("restore lost the last candidate")
	}
}

func TestLiveBacklightCapabilityAndPendingPreview(t *testing.T) {
	s, _ := newStore(t, nil)
	id := testID('a')
	other := testID('b')
	register(t, s, id, "boot", "backlight_brightness", "backlight_preview", "backlight_restore")
	register(t, s, other, "other")
	n := 36
	if _, err := s.EnqueueOptions(id, "backlight_adjust", false, &n, ""); err != ErrUnavailable {
		t.Fatalf("old capability accepted: %v", err)
	}
	register(t, s, id, "boot", "backlight_brightness", "backlight_adjust", "backlight_begin", "backlight_restore")
	for _, bad := range []int{0, 9, 101} {
		if _, err := s.EnqueueOptions(id, "backlight_adjust", false, &bad, ""); err == nil {
			t.Fatal("invalid percent accepted")
		}
	}
	c, err := s.EnqueueOptions(id, "backlight_adjust", false, &n, "")
	if err != nil || c.Percent == nil || *c.Percent != 36 {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := s.EnqueueOptions(other, "backlight_adjust", false, &n, ""); err != ErrUnavailable {
		t.Fatal("cross-display capability leak")
	}
	d := report(s, id, "boot", "backlight_brightness", "backlight_adjust", "backlight_begin", "backlight_restore")
	d.UptimeSeconds = 12
	d.BacklightReport = previewReport()
	d.CommandResult = &Result{ID: c.ID, State: "succeeded"}
	if _, err := s.Sync(d); err != nil {
		t.Fatal(err)
	}
	v, _ := s.View(id, nil)
	v.Settings.Screen.BacklightPercent = &n
	if err := s.Update(id, v.Revision, Patch{Settings: v.Settings}); err != ErrBusy {
		t.Fatalf("saved during preview: %v", err)
	}
	d.BacklightReport.Phase = "restored"
	d.BacklightReport.LastCommand = lightInt(50)
	d.BacklightReport.SampleUptimeMS = 21000
	d.UptimeSeconds = 21
	if _, err := s.Sync(d); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(id, v.Revision, Patch{Settings: v.Settings}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueOptions(id, "backlight_begin", false, &n, ""); err != nil {
		t.Fatalf("explicit start: %v", err)
	}
	// Clearing an unsupported configuration and retaining night-off must remain possible.
	register(t, s, id, "new-boot")
	v, _ = s.View(id, nil)
	v.Settings.Screen.BacklightPercent = nil
	v.Settings.Screen.NightBacklightPercent = nil
	v.Settings.Screen.NightAction = "off"
	if err := s.Update(id, v.Revision, Patch{Settings: v.Settings}); err != nil {
		t.Fatalf("clear: %v", err)
	}
}

func TestBacklightControlNegotiationAndCapabilityBounds(t *testing.T) {
	s, _ := newStore(t, nil)
	d := report(s, testID('c'), "boot")
	for i := 0; i < 18; i++ {
		d.Capabilities = append(d.Capabilities, fmt.Sprintf("cap_%d", i))
	}
	response, err := s.Sync(d)
	if err != nil || response.BacklightControlVersion != 1 {
		t.Fatalf("control negotiation: %v", err)
	}
	for len(d.Capabilities) < 25 {
		d.Capabilities = append(d.Capabilities, fmt.Sprintf("cap_%d", len(d.Capabilities)))
	}
	if _, err := s.Sync(d); err == nil {
		t.Fatal("unbounded capabilities accepted")
	}
}

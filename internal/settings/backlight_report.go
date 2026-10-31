package settings

import "errors"

// BacklightReport describes acknowledged commands, not measured luminance.
// Sample and deadline use the current device boot's monotonic clock.
type BacklightReport struct {
	Version                 int     `json:"version"`
	Phase                   string  `json:"phase"`
	LastCommand             *int    `json:"last_command"`
	OutputKnown             bool    `json:"output_known"`
	TestCommand             *int    `json:"test_command"`
	ReturnCommand           *int    `json:"return_command"`
	SampleUptimeMS          uint64  `json:"sample_uptime_ms"`
	RestoreDeadlineUptimeMS *uint64 `json:"restore_deadline_uptime_ms"`
	Error                   string  `json:"error"`
}

func validBacklightReport(b *BacklightReport, uptimeSeconds uint64) bool {
	if b == nil {
		return true
	}
	const maxInteger = uint64(1<<53 - 1)
	if b.Version != 1 || b.SampleUptimeMS > maxInteger || b.SampleUptimeMS/1000 > uptimeSeconds {
		return false
	}
	for _, command := range []*int{b.LastCommand, b.TestCommand, b.ReturnCommand} {
		if command != nil && (*command < 0 || *command > 97) {
			return false
		}
	}
	hasTest := b.TestCommand != nil
	if hasTest != (b.ReturnCommand != nil) || hasTest != (b.RestoreDeadlineUptimeMS != nil) {
		return false
	}
	if hasTest && (*b.RestoreDeadlineUptimeMS == 0 || *b.RestoreDeadlineUptimeMS > maxInteger ||
		*b.RestoreDeadlineUptimeMS > b.SampleUptimeMS+20000) {
		return false
	}
	if b.Error != "" && b.Error != "write_failed" {
		return false
	}
	switch b.Phase {
	case "unknown":
		return !b.OutputKnown && b.LastCommand == nil && !hasTest && b.Error == ""
	case "error":
		return !b.OutputKnown && b.Error == "write_failed"
	case "reference":
		return b.OutputKnown && b.LastCommand != nil && b.Error == ""
	case "preview":
		return b.OutputKnown && hasTest && b.LastCommand != nil && *b.LastCommand == *b.TestCommand && b.Error == ""
	case "restored":
		return b.OutputKnown && hasTest && b.LastCommand != nil && *b.LastCommand == *b.ReturnCommand && b.Error == ""
	default:
		return false
	}
}

func rejectOlderBacklight(old, next Device) error {
	if old.BootID == next.BootID && old.BacklightReport != nil && next.BacklightReport != nil &&
		next.BacklightReport.SampleUptimeMS < old.BacklightReport.SampleUptimeMS {
		return errors.New("stale backlight report")
	}
	return nil
}

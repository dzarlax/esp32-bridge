package model

import "strings"

// SanitizeForDisplay replaces characters unsupported by the ESP32 display fonts.
// Supported ranges: Basic Latin (0x20-0x7F), Latin-1 Supplement (0xA0-0xFF),
// Cyrillic (0x400-0x4FF). Common Unicode punctuation is transliterated to ASCII.
func SanitizeForDisplay(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 0x20 && r <= 0x7F) ||
			(r >= 0xA0 && r <= 0xFF) ||
			(r >= 0x400 && r <= 0x4FF) {
			b.WriteRune(r)
		} else if repl, ok := unicodeReplacements[r]; ok {
			b.WriteString(repl)
		} else if r == '\t' || r == '\n' {
			b.WriteByte(' ')
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

var unicodeReplacements = map[rune]string{
	'\u2013': "-",  // en-dash
	'\u2014': "-",  // em-dash
	'\u2015': "-",  // horizontal bar
	'\u2018': "'",  // left single quote
	'\u2019': "'",  // right single quote
	'\u201C': "\"", // left double quote
	'\u201D': "\"", // right double quote
	'\u2026': "...", // ellipsis
	'\u2022': "*",  // bullet
	'\u2192': "->", // right arrow
	'\u2190': "<-", // left arrow
	'\u00a0': " ",  // non-breaking space
	'\u2007': " ",  // figure space
	'\u202F': " ",  // narrow no-break space
	'\u2009': " ",  // thin space
	'\u200B': "",   // zero-width space
	'\u2011': "-",  // non-breaking hyphen
	'\u2212': "-",  // minus sign
	'\u2217': "*",  // asterisk operator
	'\u00D7': "x",  // multiplication sign
}

type HealthData struct {
	Steps     int     `json:"steps"`
	StepsPrev int     `json:"steps_prev"`
	Cal       int     `json:"cal"`
	CalPrev   int     `json:"cal_prev"`
	Sleep     float64 `json:"sleep"`
	SleepPrev float64 `json:"sleep_prev"`
	HR        int     `json:"hr"`
	RHR       int     `json:"rhr"`
	HRV       int     `json:"hrv"`
	SpO2      int     `json:"spo2"`
	Readiness int     `json:"readiness"`
}

type TaskItem struct {
	Title    string `json:"t"`
	Priority int    `json:"p"`
	Due      string `json:"due,omitempty"`
}

type NewsItem struct {
	Title    string `json:"t"`
	Category string `json:"c"`
	HoursAgo int    `json:"h"`
}

type SensorItem struct {
	Name  string `json:"n"`
	Value string `json:"v"`
	Unit  string `json:"u"`
}

type LightItem struct {
	ID         string `json:"id"`  // entity_id
	Name       string `json:"n"`
	On         bool   `json:"on"`
	Brightness int    `json:"br,omitempty"` // 0-255
}

type WeatherDaily struct {
	TempMax     float64 `json:"tmax"`
	TempMin     float64 `json:"tmin"`
	WeatherCode int     `json:"wc"`
}

type WeatherData struct {
	Temp        float64        `json:"temp"`
	Humidity    float64        `json:"hum"`
	WindSpeed   float64        `json:"wind"`
	WeatherCode int            `json:"wc"`
	Daily       []WeatherDaily `json:"daily"`
}

type TransportVehicle struct {
	LineNumber      string `json:"ln"`
	SecondsLeft     int    `json:"sl"`
	StationsBetween int    `json:"sb"`
}

type TransportStop struct {
	Vehicles []TransportVehicle `json:"vehicles"`
}

type CalendarEvent struct {
	Summary   string `json:"s"`
	StartHour int    `json:"sh"`
	StartMin  int    `json:"sm"`
	EndHour   int    `json:"eh"`
	EndMin    int    `json:"em"`
	AllDay    bool   `json:"ad,omitempty"`
	CalIdx    int    `json:"ci"`
}

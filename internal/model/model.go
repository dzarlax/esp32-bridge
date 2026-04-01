package model

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

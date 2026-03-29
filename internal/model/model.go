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

package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"esp32-bridge/internal/model"
)

type WeatherFetcher struct {
	lat, lon, tz string
	client       *http.Client
	ttl          time.Duration
}

func NewWeather(lat, lon, tz string, client *http.Client, ttl time.Duration) *WeatherFetcher {
	return &WeatherFetcher{lat: lat, lon: lon, tz: tz, client: client, ttl: ttl}
}

func (f *WeatherFetcher) Name() string      { return "weather" }
func (f *WeatherFetcher) TTL() time.Duration { return f.ttl }

func (f *WeatherFetcher) Fetch(ctx context.Context) (json.RawMessage, error) {
	u := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%s&longitude=%s"+
			"&current=temperature_2m,relative_humidity_2m,wind_speed_10m,weather_code"+
			"&daily=weather_code,temperature_2m_max,temperature_2m_min"+
			"&timezone=%s&forecast_days=5",
		f.lat, f.lon, url.QueryEscape(f.tz),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var apiResp struct {
		Current struct {
			Temp        float64 `json:"temperature_2m"`
			Humidity    float64 `json:"relative_humidity_2m"`
			WindSpeed   float64 `json:"wind_speed_10m"`
			WeatherCode int     `json:"weather_code"`
		} `json:"current"`
		Daily struct {
			WeatherCode []int     `json:"weather_code"`
			TempMax     []float64 `json:"temperature_2m_max"`
			TempMin     []float64 `json:"temperature_2m_min"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}

	out := model.WeatherData{
		Temp:        apiResp.Current.Temp,
		Humidity:    apiResp.Current.Humidity,
		WindSpeed:   apiResp.Current.WindSpeed,
		WeatherCode: apiResp.Current.WeatherCode,
	}
	n := len(apiResp.Daily.WeatherCode)
	if n > len(apiResp.Daily.TempMax) {
		n = len(apiResp.Daily.TempMax)
	}
	if n > len(apiResp.Daily.TempMin) {
		n = len(apiResp.Daily.TempMin)
	}
	for i := 0; i < n; i++ {
		out.Daily = append(out.Daily, model.WeatherDaily{
			TempMax:     apiResp.Daily.TempMax[i],
			TempMin:     apiResp.Daily.TempMin[i],
			WeatherCode: apiResp.Daily.WeatherCode[i],
		})
	}

	return json.Marshal(out)
}

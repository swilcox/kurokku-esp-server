package solar

import (
	"testing"
	"time"
)

func TestCalculate_NYC_SummerSolstice(t *testing.T) {
	// NYC: 40.7128 N, 74.0060 W on June 20, 2025
	loc, _ := time.LoadLocation("America/New_York")
	date := time.Date(2025, 6, 20, 12, 0, 0, 0, loc)
	st := Calculate(date, 40.7128, -74.0060)

	// Sunrise around 5:25 AM EDT, Sunset around 8:31 PM EDT
	expectSunriseHour := 5
	expectSunsetHour := 20

	if st.Sunrise.Hour() != expectSunriseHour {
		t.Errorf("sunrise hour = %d, want %d (full: %s)", st.Sunrise.Hour(), expectSunriseHour, st.Sunrise.Format(time.RFC3339))
	}
	if st.Sunset.Hour() != expectSunsetHour {
		t.Errorf("sunset hour = %d, want %d (full: %s)", st.Sunset.Hour(), expectSunsetHour, st.Sunset.Format(time.RFC3339))
	}
}

func TestCalculate_NYC_WinterSolstice(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	date := time.Date(2025, 12, 21, 12, 0, 0, 0, loc)
	st := Calculate(date, 40.7128, -74.0060)

	// Sunrise around 7:16 AM EST, Sunset around 4:32 PM EST
	expectSunriseHour := 7
	expectSunsetHour := 16

	if st.Sunrise.Hour() != expectSunriseHour {
		t.Errorf("sunrise hour = %d, want %d (full: %s)", st.Sunrise.Hour(), expectSunriseHour, st.Sunrise.Format(time.RFC3339))
	}
	if st.Sunset.Hour() != expectSunsetHour {
		t.Errorf("sunset hour = %d, want %d (full: %s)", st.Sunset.Hour(), expectSunsetHour, st.Sunset.Format(time.RFC3339))
	}
}

func TestCalculate_SouthernHemisphere(t *testing.T) {
	// Sydney: -33.8688 S, 151.2093 E on Dec 21 (summer there)
	loc, _ := time.LoadLocation("Australia/Sydney")
	date := time.Date(2025, 12, 21, 12, 0, 0, 0, loc)
	st := Calculate(date, -33.8688, 151.2093)

	// Sunrise ~5:42 AM, Sunset ~8:07 PM AEDT
	if st.Sunrise.Hour() != 5 {
		t.Errorf("Sydney summer sunrise hour = %d, want 5 (full: %s)", st.Sunrise.Hour(), st.Sunrise.Format(time.RFC3339))
	}
	if st.Sunset.Hour() != 20 {
		t.Errorf("Sydney summer sunset hour = %d, want 20 (full: %s)", st.Sunset.Hour(), st.Sunset.Format(time.RFC3339))
	}
}

func TestIsDaytime_Noon(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	noon := time.Date(2025, 6, 20, 12, 0, 0, 0, loc)
	if !IsDaytime(noon, 40.7128, -74.0060) {
		t.Error("noon should be daytime")
	}
}

func TestIsDaytime_Midnight(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	midnight := time.Date(2025, 6, 20, 2, 0, 0, 0, loc)
	if IsDaytime(midnight, 40.7128, -74.0060) {
		t.Error("2 AM should not be daytime")
	}
}

func TestIsDaytime_JustAfterSunrise(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	// Summer in NYC, sunrise ~5:25 AM. 6 AM should be daytime.
	morning := time.Date(2025, 6, 20, 6, 0, 0, 0, loc)
	if !IsDaytime(morning, 40.7128, -74.0060) {
		t.Error("6 AM in summer NYC should be daytime")
	}
}

func TestIsDaytime_JustAfterSunset(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	// Summer in NYC, sunset ~8:31 PM. 9 PM should not be daytime.
	evening := time.Date(2025, 6, 20, 21, 0, 0, 0, loc)
	if IsDaytime(evening, 40.7128, -74.0060) {
		t.Error("9 PM in summer NYC should not be daytime")
	}
}

func TestCalculate_SunriseBeforeSunset(t *testing.T) {
	// For any normal latitude, sunrise should be before sunset
	loc := time.UTC
	dates := []time.Time{
		time.Date(2025, 3, 20, 12, 0, 0, 0, loc),
		time.Date(2025, 6, 21, 12, 0, 0, 0, loc),
		time.Date(2025, 9, 22, 12, 0, 0, 0, loc),
		time.Date(2025, 12, 21, 12, 0, 0, 0, loc),
	}
	for _, d := range dates {
		st := Calculate(d, 40.7128, -74.0060)
		if !st.Sunrise.Before(st.Sunset) {
			t.Errorf("on %s: sunrise %s should be before sunset %s",
				d.Format("2006-01-02"), st.Sunrise.Format(time.RFC3339), st.Sunset.Format(time.RFC3339))
		}
	}
}

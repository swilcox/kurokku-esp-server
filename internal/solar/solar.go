package solar

import (
	"math"
	"time"
)

// SunTimes holds the sunrise and sunset for a given day and location.
type SunTimes struct {
	Sunrise time.Time
	Sunset  time.Time
}

// Calculate returns sunrise and sunset times for the given time's date and location.
// Uses the NOAA solar position algorithm. The returned times are in the same
// timezone as the input time. For polar day/night conditions where there is no
// sunrise or sunset, Sunrise is set to the start of the day and Sunset to the
// end (polar day) or both to the start of the day (polar night).
func Calculate(t time.Time, lat, lon float64) SunTimes {
	loc := t.Location()
	year, month, day := t.Date()
	startOfDay := time.Date(year, month, day, 0, 0, 0, 0, loc)

	jd := julianDay(year, int(month), day)
	jc := (jd - 2451545.0) / 36525.0

	// Solar geometry
	geomMeanLonSun := math.Mod(280.46646+jc*(36000.76983+0.0003032*jc), 360)
	geomMeanAnomSun := 357.52911 + jc*(35999.05029-0.0001537*jc)
	eccentEarthOrbit := 0.016708634 - jc*(0.000042037+0.0000001267*jc)

	sunEqOfCtr := math.Sin(degToRad(geomMeanAnomSun))*(1.914602-jc*(0.004817+0.000014*jc)) +
		math.Sin(degToRad(2*geomMeanAnomSun))*(0.019993-0.000101*jc) +
		math.Sin(degToRad(3*geomMeanAnomSun))*0.000289

	sunTrueLon := geomMeanLonSun + sunEqOfCtr
	sunAppLon := sunTrueLon - 0.00569 - 0.00478*math.Sin(degToRad(125.04-1934.136*jc))

	meanObliqEcliptic := 23 + (26+((21.448-jc*(46.815+jc*(0.00059-jc*0.001813))))/60)/60
	obliqCorr := meanObliqEcliptic + 0.00256*math.Cos(degToRad(125.04-1934.136*jc))

	sunDeclin := radToDeg(math.Asin(math.Sin(degToRad(obliqCorr)) * math.Sin(degToRad(sunAppLon))))

	varY := math.Tan(degToRad(obliqCorr / 2))
	varY *= varY

	eqOfTime := 4 * radToDeg(
		varY*math.Sin(2*degToRad(geomMeanLonSun))-
			2*eccentEarthOrbit*math.Sin(degToRad(geomMeanAnomSun))+
			4*eccentEarthOrbit*varY*math.Sin(degToRad(geomMeanAnomSun))*math.Cos(2*degToRad(geomMeanLonSun))-
			0.5*varY*varY*math.Sin(4*degToRad(geomMeanLonSun))-
			1.25*eccentEarthOrbit*eccentEarthOrbit*math.Sin(2*degToRad(geomMeanAnomSun)),
	)

	// Hour angle
	latRad := degToRad(lat)
	declinRad := degToRad(sunDeclin)
	cosHA := (math.Cos(degToRad(90.833))/(math.Cos(latRad)*math.Cos(declinRad)) - math.Tan(latRad)*math.Tan(declinRad))

	// Polar day or polar night
	if cosHA < -1 {
		// Sun never sets (polar day)
		return SunTimes{
			Sunrise: startOfDay,
			Sunset:  startOfDay.Add(24*time.Hour - time.Second),
		}
	}
	if cosHA > 1 {
		// Sun never rises (polar night)
		return SunTimes{
			Sunrise: startOfDay,
			Sunset:  startOfDay,
		}
	}

	ha := radToDeg(math.Acos(cosHA))

	// Solar noon in minutes from midnight UTC
	solarNoonMin := (720 - 4*lon - eqOfTime) // minutes UTC

	sunriseMin := solarNoonMin - ha*4
	sunsetMin := solarNoonMin + ha*4

	_, offset := t.Zone()
	tzOffsetMin := float64(offset) / 60.0

	sunrise := startOfDay.Add(time.Duration((sunriseMin+tzOffsetMin)*60) * time.Second)
	sunset := startOfDay.Add(time.Duration((sunsetMin+tzOffsetMin)*60) * time.Second)

	return SunTimes{
		Sunrise: sunrise,
		Sunset:  sunset,
	}
}

// IsDaytime returns true if the given time is between sunrise and sunset
// for the given location.
func IsDaytime(t time.Time, lat, lon float64) bool {
	st := Calculate(t, lat, lon)
	return !t.Before(st.Sunrise) && t.Before(st.Sunset)
}

func julianDay(year, month, day int) float64 {
	if month <= 2 {
		year--
		month += 12
	}
	a := year / 100
	b := 2 - a + a/4
	return float64(int(365.25*float64(year+4716))) + float64(int(30.6001*float64(month+1))) + float64(day) + float64(b) - 1524.5
}

func degToRad(d float64) float64 { return d * math.Pi / 180 }
func radToDeg(r float64) float64 { return r * 180 / math.Pi }

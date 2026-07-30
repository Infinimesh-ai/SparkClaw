package infinimeshinfo

const (
	WeatherGranularityCurrent WeatherGranularity = "current"
	WeatherGranularityHourly  WeatherGranularity = "hourly"
	WeatherGranularityDaily   WeatherGranularity = "daily"

	WeatherUnitsMetric WeatherUnits = "metric"

	// MaxHourlyForecastHours bounds both the requestable hourly steps and the
	// hourly entries accepted in a response; MaxDailyForecastDays likewise for
	// requested days and daily entries.
	MaxHourlyForecastHours = 48
	MaxDailyForecastDays   = 7
)

const (
	WeatherConditionClear        WeatherCondition = "clear"
	WeatherConditionPartlyCloudy WeatherCondition = "partly_cloudy"
	WeatherConditionCloudy       WeatherCondition = "cloudy"
	WeatherConditionHaze         WeatherCondition = "haze"
	WeatherConditionFog          WeatherCondition = "fog"
	WeatherConditionDust         WeatherCondition = "dust"
	WeatherConditionSand         WeatherCondition = "sand"
	WeatherConditionWind         WeatherCondition = "wind"
	WeatherConditionLightRain    WeatherCondition = "light_rain"
	WeatherConditionModerateRain WeatherCondition = "moderate_rain"
	WeatherConditionHeavyRain    WeatherCondition = "heavy_rain"
	WeatherConditionStormRain    WeatherCondition = "storm_rain"
	WeatherConditionLightSnow    WeatherCondition = "light_snow"
	WeatherConditionModerateSnow WeatherCondition = "moderate_snow"
	WeatherConditionHeavySnow    WeatherCondition = "heavy_snow"
	WeatherConditionStormSnow    WeatherCondition = "storm_snow"
	WeatherConditionUnknown      WeatherCondition = "unknown"
)

// AllWeatherConditions is the closed condition set of the weather contract.
// Response validation accepts exactly these values, so consumers rendering
// conditions can (and should) be exhaustive over this list.
var AllWeatherConditions = []WeatherCondition{
	WeatherConditionClear, WeatherConditionPartlyCloudy, WeatherConditionCloudy,
	WeatherConditionHaze, WeatherConditionFog, WeatherConditionDust,
	WeatherConditionSand, WeatherConditionWind,
	WeatherConditionLightRain, WeatherConditionModerateRain,
	WeatherConditionHeavyRain, WeatherConditionStormRain,
	WeatherConditionLightSnow, WeatherConditionModerateSnow,
	WeatherConditionHeavySnow, WeatherConditionStormSnow,
	WeatherConditionUnknown,
}

type WeatherGranularity string
type WeatherUnits string
type WeatherCondition string

type WeatherRequest struct {
	Location    WeatherRequestLocation
	Granularity []WeatherGranularity
	Days        int
	HourlySteps int
	Units       WeatherUnits
	Language    string
}

type WeatherRequestLocation struct {
	Name      string
	Latitude  *float64
	Longitude *float64
}

type WeatherResponse struct {
	RequestID string          `json:"request_id"`
	Status    string          `json:"status"`
	Weather   WeatherReport   `json:"weather"`
	Sources   []WeatherSource `json:"sources"`
	Usage     Usage           `json:"usage"`
}

type WeatherReport struct {
	Provider   string             `json:"provider"`
	Location   WeatherCoordinates `json:"location"`
	Timezone   string             `json:"timezone"`
	ObservedAt string             `json:"observed_at"`
	Current    *WeatherCurrent    `json:"current"`
	Hourly     []WeatherHour      `json:"hourly"`
	Daily      []WeatherDay       `json:"daily"`
	AirQuality *WeatherAirQuality `json:"air_quality"`
	Alerts     []WeatherAlert     `json:"alerts"`
}

type WeatherCoordinates struct {
	Latitude  *float64 `json:"lat"`
	Longitude *float64 `json:"lon"`
	Name      string   `json:"name"`
}

type WeatherCurrent struct {
	TemperatureC         *float64         `json:"temp_c"`
	ApparentTemperatureC *float64         `json:"apparent_temp_c"`
	Condition            WeatherCondition `json:"condition"`
	HumidityPercent      *float64         `json:"humidity_percent"`
	CloudCoverPercent    *float64         `json:"cloud_cover_percent"`
	PrecipitationMMH     *float64         `json:"precipitation_mm_h"`
	WindSpeedKPH         *float64         `json:"wind_speed_kph"`
	WindDirectionDegrees *float64         `json:"wind_direction_deg"`
	PressureHPa          *float64         `json:"pressure_hpa"`
	VisibilityKM         *float64         `json:"visibility_km"`
}

type WeatherHour struct {
	Time                            string           `json:"time"`
	TemperatureC                    *float64         `json:"temp_c"`
	Condition                       WeatherCondition `json:"condition"`
	PrecipitationMMH                *float64         `json:"precipitation_mm_h"`
	PrecipitationProbabilityPercent *float64         `json:"precipitation_probability_percent"`
	WindSpeedKPH                    *float64         `json:"wind_speed_kph"`
	HumidityPercent                 *float64         `json:"humidity_percent"`
}

type WeatherDay struct {
	Date                            string           `json:"date"`
	TemperatureMaxC                 *float64         `json:"temp_max_c"`
	TemperatureMinC                 *float64         `json:"temp_min_c"`
	Condition                       WeatherCondition `json:"condition"`
	PrecipitationMM                 *float64         `json:"precipitation_mm"`
	PrecipitationProbabilityPercent *float64         `json:"precipitation_probability_percent"`
	WindSpeedKPH                    *float64         `json:"wind_speed_kph"`
	HumidityPercent                 *float64         `json:"humidity_percent"`
	Sunrise                         string           `json:"sunrise"`
	Sunset                          string           `json:"sunset"`
}

type WeatherAirQuality struct {
	PM25     *float64 `json:"pm25"`
	PM10     *float64 `json:"pm10"`
	O3       *float64 `json:"o3"`
	SO2      *float64 `json:"so2"`
	NO2      *float64 `json:"no2"`
	CO       *float64 `json:"co"`
	AQIChina *int     `json:"aqi_chn"`
	AQIUSA   *int     `json:"aqi_usa"`
}

type WeatherAlert struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Description string `json:"description"`
	PublishedAt string `json:"published_at"`
}

type WeatherSource struct {
	ID          string `json:"id"`
	SourceType  string `json:"source_type"`
	Provider    string `json:"provider"`
	RetrievedAt string `json:"retrieved_at"`
}

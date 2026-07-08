package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/pkg/toolx"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

type WeatherTool struct{}

func NewWeatherTool() *WeatherTool { return &WeatherTool{} }

func (t *WeatherTool) Spec() toolx.ToolSpec { return toolx.BuiltinWeather }
func (t *WeatherTool) Name() string         { return toolx.BuiltinWeather.Name }
func (t *WeatherTool) Code() string         { return toolx.BuiltinWeather.Code }

func (t *WeatherTool) Enabled(ctx registry.Context) bool { return true }

func (t *WeatherTool) Build(ctx registry.Context) (einotool.BaseTool, error) {
	return &WeatherTool{}, nil
}

func (t *WeatherTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: toolx.BuiltinWeather.Name,
		Desc: "查询天气工具。根据 location 和 date 查询实时/预报天气，适合回答今天、明天、未来几天的天气、温度、降雨、风力等问题。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&einojsonschema.Schema{
			Version: einojsonschema.Version,
			Type:    "object",
			Required: []string{"location"},
			Properties: orderedmap.New[string, *einojsonschema.Schema](orderedmap.WithInitialData(
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "location", Value: &einojsonschema.Schema{Type: "string", Description: "城市或地点，例如：合肥、上海、北京朝阳。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "date", Value: &einojsonschema.Schema{Type: "string", Description: "日期，例如 today、tomorrow 或 YYYY-MM-DD；默认 today。"}},
			)),
		}),
		Extra: map[string]any{"toolCode": toolx.BuiltinWeather.Code, "sourceType": toolx.BuiltinWeather.SourceType},
	}, nil
}

type weatherArgs struct {
	Location string `json:"location"`
	Date     string `json:"date"`
}

func (t *WeatherTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	var args weatherArgs
	if strings.TrimSpace(argumentsInJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", fmt.Errorf("天气工具参数 JSON 不合法: %w", err)
		}
	}
	location := strings.TrimSpace(args.Location)
	if location == "" {
		return `{"status":"need_location","message":"需要先确认要查询哪个城市或地点。"}`, nil
	}
	date := normalizeWeatherDate(args.Date)
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	result, err := fetchWttrWeather(ctx, location, date)
	if err != nil {
		result, err = fetchChinaWeather(ctx, location, date)
	}
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func normalizeWeatherDate(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "today", "今天":
		return "today"
	case "tomorrow", "明天":
		return "tomorrow"
	default:
		return value
	}
}

var chinaWeatherCityCodes = map[string]string{
	"北京": "101010100",
	"上海": "101020100",
	"广州": "101280101",
	"深圳": "101280601",
	"合肥": "101220101",
	"长沙": "101250101",
	"南京": "101190101",
	"杭州": "101210101",
	"成都": "101270101",
	"重庆": "101040100",
	"武汉": "101200101",
	"西安": "101110101",
	"郑州": "101180101",
	"济南": "101120101",
	"青岛": "101120201",
	"苏州": "101190401",
	"厦门": "101230201",
	"福州": "101230101",
	"天津": "101030100",
	"沈阳": "101070101",
	"大连": "101070201",
	"哈尔滨": "101050101",
	"长春": "101060101",
	"石家庄": "101090101",
	"太原": "101100101",
	"南昌": "101240101",
	"南宁": "101300101",
	"昆明": "101290101",
	"贵阳": "101260101",
	"海口": "101310101",
	"三亚": "101310201",
	"拉萨": "101140101",
	"兰州": "101160101",
	"银川": "101170101",
	"西宁": "101150101",
	"乌鲁木齐": "101130101",
	"呼和浩特": "101080101",
}

func fetchChinaWeather(ctx context.Context, location string, date string) (weatherResult, error) {
	city := normalizeChinaWeatherCity(location)
	code := chinaWeatherCityCodes[city]
	if code == "" {
		return weatherResult{}, fmt.Errorf("中国天气网暂未内置城市码: %s", location)
	}
	endpoint := "https://www.weather.com.cn/weather/" + code + ".shtml"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return weatherResult{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.weather.com.cn/")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return weatherResult{}, fmt.Errorf("中国天气网查询失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return weatherResult{}, fmt.Errorf("中国天气网查询失败，HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return weatherResult{}, err
	}
	days := parseChinaWeatherDays(string(body))
	if len(days) == 0 {
		return weatherResult{}, fmt.Errorf("中国天气网响应未解析到预报")
	}
	idx := chooseChinaWeatherDayIndex(date, len(days))
	day := days[idx]
	return weatherResult{
		Status:       "ok",
		Location:     city,
		Date:         normalizeWeatherDate(date),
		Condition:    day.Weather,
		ForecastText: formatChinaWeatherForecast(day),
		Source:       "weather.com.cn",
	}, nil
}

func normalizeChinaWeatherCity(location string) string {
	city := strings.TrimSpace(location)
	city = strings.TrimSuffix(city, "市")
	city = strings.TrimSuffix(city, "区")
	city = strings.TrimSuffix(city, "县")
	return city
}

type chinaWeatherDay struct {
	Date      string
	Weather   string
	TempHigh  string
	TempLow   string
	Wind      string
	WindLevel string
}

func parseChinaWeatherDays(page string) []chinaWeatherDay {
	start := strings.Index(page, `id="7d"`)
	if start < 0 {
		start = strings.Index(page, `id="hidden_title"`)
	}
	if start >= 0 {
		page = page[start:]
	}
	liRe := regexp.MustCompile(`(?s)<li[^>]*>(.*?)</li>`)
	dateRe := regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	weatherRe := regexp.MustCompile(`(?s)<p[^>]*class="wea"[^>]*title="([^"]*)"`)
	weatherTextRe := regexp.MustCompile(`(?s)<p[^>]*class="wea"[^>]*>(.*?)</p>`)
	highRe := regexp.MustCompile(`(?s)<span>(-?\d+)</span>\s*<i>℃</i>`)
	lowRe := regexp.MustCompile(`(?s)<i>(-?\d+)℃</i>`)
	windRe := regexp.MustCompile(`(?s)<em>\s*<span[^>]*title="([^"]*)"`)
	windLevelRe := regexp.MustCompile(`(?s)</em>\s*<i>([^<]+)</i>`)
	days := make([]chinaWeatherDay, 0, 7)
	for _, m := range liRe.FindAllStringSubmatch(page, 7) {
		block := m[1]
		date := cleanHTML(dateRe.FindStringSubmatch(block))
		if date == "" {
			continue
		}
		weather := cleanFirstSubmatch(weatherRe.FindStringSubmatch(block))
		if weather == "" {
			weather = cleanHTML(weatherTextRe.FindStringSubmatch(block))
		}
		high := cleanFirstSubmatch(highRe.FindStringSubmatch(block))
		low := cleanFirstSubmatch(lowRe.FindStringSubmatch(block))
		wind := cleanFirstSubmatch(windRe.FindStringSubmatch(block))
		windLevel := cleanFirstSubmatch(windLevelRe.FindStringSubmatch(block))
		days = append(days, chinaWeatherDay{Date: date, Weather: weather, TempHigh: high, TempLow: low, Wind: wind, WindLevel: windLevel})
	}
	return days
}

func cleanHTML(match []string) string {
	if len(match) < 2 {
		return ""
	}
	text := regexp.MustCompile(`(?s)<[^>]+>`).ReplaceAllString(match[1], "")
	return strings.TrimSpace(html.UnescapeString(text))
}

func cleanFirstSubmatch(match []string) string {
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(match[1]))
}

func chooseChinaWeatherDayIndex(date string, count int) int {
	if count <= 1 {
		return 0
	}
	date = normalizeWeatherDate(date)
	switch date {
	case "tomorrow":
		return 1
	default:
		return 0
	}
}

func formatChinaWeatherForecast(day chinaWeatherDay) string {
	temp := ""
	if day.TempHigh != "" || day.TempLow != "" {
		if day.TempHigh != "" && day.TempLow != "" {
			temp = day.TempLow + "-" + day.TempHigh + "℃"
		} else if day.TempHigh != "" {
			temp = "最高" + day.TempHigh + "℃"
		} else {
			temp = "最低" + day.TempLow + "℃"
		}
	}
	parts := []string{day.Date, day.Weather, temp, strings.TrimSpace(day.Wind + " " + day.WindLevel)}
	ret := make([]string, 0, len(parts))
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item != "" {
			ret = append(ret, item)
		}
	}
	return strings.Join(ret, "，")
}

type wttrResponse struct {
	CurrentCondition []struct {
		TempC       string `json:"temp_C"`
		FeelsLikeC  string `json:"FeelsLikeC"`
		Humidity    string `json:"humidity"`
		WeatherDesc []struct{ Value string `json:"value"` } `json:"weatherDesc"`
		WindDir16Point string `json:"winddir16Point"`
		WindspeedKmph  string `json:"windspeedKmph"`
		PrecipMM       string `json:"precipMM"`
	} `json:"current_condition"`
	Weather []struct {
		Date     string `json:"date"`
		AvgTempC string `json:"avgtempC"`
		MaxTempC string `json:"maxtempC"`
		MinTempC string `json:"mintempC"`
		Hourly   []struct {
			ChanceOfRain string `json:"chanceofrain"`
			WeatherDesc  []struct{ Value string `json:"value"` } `json:"weatherDesc"`
		} `json:"hourly"`
	} `json:"weather"`
}

type weatherResult struct {
	Status       string `json:"status"`
	Location     string `json:"location"`
	Date         string `json:"date"`
	Condition    string `json:"condition,omitempty"`
	TempC        string `json:"tempC,omitempty"`
	FeelsLikeC   string `json:"feelsLikeC,omitempty"`
	Humidity     string `json:"humidity,omitempty"`
	Wind         string `json:"wind,omitempty"`
	PrecipMM     string `json:"precipMM,omitempty"`
	ForecastText string `json:"forecastText,omitempty"`
	Source       string `json:"source"`
}

func fetchWttrWeather(ctx context.Context, location string, date string) (weatherResult, error) {
	endpoint := "https://wttr.in/" + url.PathEscape(location) + "?format=j1&lang=zh"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return weatherResult{}, err
	}
	req.Header.Set("User-Agent", "agent-desk-weather-tool/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return weatherResult{}, fmt.Errorf("天气查询失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return weatherResult{}, fmt.Errorf("天气查询失败，HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return weatherResult{}, err
	}
	var parsed wttrResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return weatherResult{}, fmt.Errorf("天气响应解析失败: %w", err)
	}
	ret := weatherResult{Status: "ok", Location: location, Date: date, Source: "wttr.in"}
	if len(parsed.CurrentCondition) > 0 {
		cur := parsed.CurrentCondition[0]
		ret.TempC = cur.TempC
		ret.FeelsLikeC = cur.FeelsLikeC
		ret.Humidity = cur.Humidity
		ret.PrecipMM = cur.PrecipMM
		if cur.WindDir16Point != "" || cur.WindspeedKmph != "" {
			ret.Wind = strings.TrimSpace(cur.WindDir16Point + " " + cur.WindspeedKmph + "km/h")
		}
		if len(cur.WeatherDesc) > 0 {
			ret.Condition = cur.WeatherDesc[0].Value
		}
	}
	if len(parsed.Weather) > 0 {
		idx := 0
		if date == "tomorrow" && len(parsed.Weather) > 1 {
			idx = 1
		}
		day := parsed.Weather[idx]
		condition := ""
		rain := ""
		if len(day.Hourly) > 0 {
			mid := day.Hourly[len(day.Hourly)/2]
			if len(mid.WeatherDesc) > 0 {
				condition = mid.WeatherDesc[0].Value
			}
			rain = mid.ChanceOfRain
		}
		ret.ForecastText = fmt.Sprintf("%s：%s-%s°C，平均%s°C，%s，降雨概率%s%%", day.Date, day.MinTempC, day.MaxTempC, day.AvgTempC, condition, rain)
	}
	return ret, nil
}

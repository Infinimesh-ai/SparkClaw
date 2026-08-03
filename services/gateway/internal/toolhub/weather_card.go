package toolhub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

const (
	weatherCardWidth  = 900
	weatherCardHeight = 1200
)

var weatherCardLocation = mustWeatherCardLocation()

type weatherCardData struct {
	Location    string
	UpdatedAt   string
	Condition   string
	Temperature string
	MissingData bool
	FeelsLike   string
	Humidity    string
	Wind        string
	Precip      string
	UV          string
	Suggestion  string
	Forecast    []weatherForecastDay
	Hourly      []weatherForecastHour
	Source      string
}

type weatherForecastDay struct {
	Date      string
	MinTemp   string
	MaxTemp   string
	Condition string
	Rain      string
}

type weatherForecastHour struct {
	Time      string
	Temp      string
	Condition string
	Rain      string
}

type weatherCardFaces struct {
	Title font.Face
	Hero  font.Face
	H2    font.Face
	Body  font.Face
	Small font.Face
	Icon  font.Face
}

func (h *ToolHub) renderWeatherCard(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	_ = ctx
	payloadRef := strings.TrimSpace(stringArg(args, "weather_payload_ref", ""))
	if payloadRef == "" || h.store == nil {
		return Result{}, errors.New("weather card requires a governed weather payload reference")
	}
	call, ok := h.store.GetToolCall(payloadRef)
	if !ok || call.SessionID != sessionID || call.RunID != runID {
		return Result{}, errors.New("weather payload reference is outside the current run")
	}
	payload, err := weatherPayloadFromCall(call)
	if err != nil {
		return Result{}, err
	}
	data := weatherCardDataFromPayload(payload)
	if strings.TrimSpace(data.UpdatedAt) == "" {
		data.UpdatedAt = weatherCardNow().Format("2006-01-02 15:04")
	}
	if strings.TrimSpace(data.Suggestion) == "" {
		data.Suggestion = weatherSuggestion(data)
	}
	img, err := drawWeatherCard(data)
	if err != nil {
		return Result{}, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return Result{}, err
	}
	relPath, absPath, err := h.writeMediaPNG(buf.Bytes(), sessionID, "weather_card")
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	sum := sha256.Sum256(buf.Bytes())
	object := app.ArtifactObject{
		ID:          app.NewID("obj"),
		Kind:        "media_weather_card",
		RunID:       runID,
		SessionID:   sessionID,
		Backend:     "workspace",
		Key:         relPath,
		URI:         "workspace://" + filepath.ToSlash(relPath),
		Path:        absPath,
		ContentType: "image/png",
		Bytes:       len(buf.Bytes()),
		CreatedAt:   now,
	}
	if h.store != nil {
		h.store.SaveArtifactObject(object)
	}
	return Result{Output: map[string]any{
		"status":       "completed",
		"media_path":   filepath.ToSlash(relPath),
		"path":         absPath,
		"uri":          object.URI,
		"artifact_id":  object.ID,
		"content_type": "image/png",
		"bytes":        len(buf.Bytes()),
		"width":        weatherCardWidth,
		"height":       weatherCardHeight,
		"sha256":       hex.EncodeToString(sum[:]),
		"summary":      fmt.Sprintf("%s天气卡片", data.Location),
		"untrusted":    true,
	}}, nil
}

func (h *ToolHub) writeMediaPNG(raw []byte, sessionID, prefix string) (string, string, error) {
	root := strings.TrimSpace(h.cfg.Workspaces.DefaultRoot)
	if strings.TrimSpace(sessionID) != "" && h.store != nil {
		if session, ok := h.store.GetSession(sessionID); ok && strings.TrimSpace(session.WorkspaceRoot) != "" {
			root = strings.TrimSpace(session.WorkspaceRoot)
		}
	}
	if root == "" {
		return "", "", errors.New("workspace root is not configured")
	}
	relPath := filepath.Join("media", time.Now().UTC().Format("20060102"), app.NewID(prefix)+".png")
	absPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(absPath, raw, 0o644); err != nil {
		return "", "", err
	}
	return filepath.ToSlash(relPath), absPath, nil
}

func drawWeatherCard(data weatherCardData) (image.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, weatherCardWidth, weatherCardHeight))
	drawVerticalGradient(img, color.RGBA{70, 126, 208, 255}, color.RGBA{165, 202, 235, 255})
	faces, err := loadWeatherCardFaces(weatherCardFontPaths())
	if err != nil {
		return nil, fmt.Errorf("weather card font unavailable: %w", err)
	}

	temp := displayTemperature(data.Temperature)
	condition := displayCondition(data.Condition)
	weatherKind := classifyWeatherKind(data)
	alertTitle, _ := weatherAlert(data, weatherKind)
	tipTitle, tipBody := weatherWarmTip(data, weatherKind)

	white := color.RGBA{255, 255, 255, 255}
	softWhite := color.RGBA{232, 242, 255, 255}
	card := color.RGBA{70, 135, 220, 184}

	drawCenteredText(img, faces.Body, weatherCardWidth/2, 86, fallbackText(data.Location, "天气"), white)
	drawCenteredTemperature(img, faces, weatherCardWidth/2, 318, temp, white)
	drawCenteredText(img, faces.H2, weatherCardWidth/2, 444, weatherSummaryLine(data, condition), white)
	drawAlertPill(img, faces, weatherCardWidth/2, 540, alertTitle)

	drawGlassCard(img, 52, 670, 796, 246, 46, card)
	drawText(img, faces.H2, 92, 746, tipTitle, white)
	for i, line := range splitCardLines(tipBody, 17) {
		if i >= 3 {
			break
		}
		drawText(img, faces.Body, 92, 812+i*56, line, softWhite)
	}

	drawHourlyForecastCard(img, faces, 52, 946, 796, 214, data, weatherKind)

	return img, nil
}

func weatherCardFontPaths() []string {
	return []string{
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/google-noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJKsc-Regular.otf",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
		"/usr/share/fonts/truetype/arphic/ukai.ttc",
		"/usr/share/fonts/truetype/arphic/uming.ttc",
		"/System/Library/Fonts/PingFang.ttc",
		"/System/Library/Fonts/STHeiti Medium.ttc",
		"/System/Library/Fonts/STHeiti Light.ttc",
		"/System/Library/Fonts/Hiragino Sans GB.ttc",
		"/System/Library/Fonts/Supplemental/Songti.ttc",
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
		"/System/Library/Fonts/Supplemental/Arial.ttf",
		"/System/Library/Fonts/SFNS.ttf",
	}
}

func loadWeatherCardFaces(paths []string) (weatherCardFaces, error) {
	parsed, err := firstWeatherCardFont(paths)
	if err != nil {
		return weatherCardFaces{}, err
	}
	faces := weatherCardFaces{}
	for _, entry := range []struct {
		destination *font.Face
		size        float64
	}{
		{destination: &faces.Title, size: 76},
		{destination: &faces.Hero, size: 228},
		{destination: &faces.H2, size: 38},
		{destination: &faces.Body, size: 30},
		{destination: &faces.Small, size: 24},
		{destination: &faces.Icon, size: 60},
	} {
		face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: entry.size, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return weatherCardFaces{}, fmt.Errorf("create %.0fpx font face: %w", entry.size, err)
		}
		*entry.destination = face
	}
	return faces, nil
}

func drawCenteredTemperature(img *image.RGBA, faces weatherCardFaces, cx, baseline int, value string, c color.Color) {
	number, unit := splitTemperatureDisplay(value)
	total := textAdvance(faces.Hero, number) + textAdvance(faces.H2, unit) + 18
	x := cx - total/2
	drawText(img, faces.Hero, x, baseline, number, c)
	drawText(img, faces.H2, x+textAdvance(faces.Hero, number)+18, baseline-126, unit, c)
}

func weatherSummaryLine(data weatherCardData, condition string) string {
	low, high := weatherTempRange(data)
	parts := []string{condition}
	if low != "" || high != "" {
		parts = append(parts, strings.TrimSpace(low+" / "+high))
	}
	if data.UV != "" {
		parts = append(parts, "紫外线 "+strings.TrimSpace(data.UV))
	}
	return strings.Join(parts, "  ")
}

func weatherTempRange(data weatherCardData) (string, string) {
	date := weatherCardNow().Format("2006-01-02")
	if reference, ok := weatherTimestamp(data.UpdatedAt); ok {
		date = reference.In(weatherCardLocation).Format("2006-01-02")
	}
	for _, day := range data.Forecast {
		dayDate, ok := weatherForecastDate(day.Date)
		if !ok || dayDate != date {
			continue
		}
		current, hasCurrent := weatherNumber(data.Temperature)
		low, hasLow := weatherNumber(day.MinTemp)
		high, hasHigh := weatherNumber(day.MaxTemp)
		if hasCurrent && hasLow && current < low {
			return "", ""
		}
		if hasCurrent && hasHigh && current > high {
			return "", ""
		}
		return displayTemperature(day.MinTemp), displayTemperature(day.MaxTemp)
	}
	return "", ""
}

func weatherForecastDate(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if parsed, ok := weatherTimestamp(value); ok {
		return parsed.In(weatherCardLocation).Format("2006-01-02"), true
	}
	if parsed, err := time.ParseInLocation("2006-01-02", value, weatherCardLocation); err == nil {
		return parsed.Format("2006-01-02"), true
	}
	return "", false
}

func drawAlertPill(img *image.RGBA, faces weatherCardFaces, cx, cy int, text string) {
	if strings.TrimSpace(text) == "" {
		text = "天气提醒"
	}
	w := 360
	x := cx - w/2
	fillRoundedRect(img, x, cy-48, w, 78, 39, color.RGBA{58, 126, 216, 190})
	drawCircle(img, x+58, cy-9, 32, color.RGBA{255, 190, 24, 255})
	drawText(img, faces.H2, x+45, cy+6, "!", color.RGBA{255, 255, 255, 255})
	drawText(img, faces.H2, x+102, cy+5, text, color.RGBA{255, 255, 255, 255})
}

func drawGlassCard(img *image.RGBA, x, y, w, h, r int, c color.RGBA) {
	fillRoundedRect(img, x, y, w, h, r, c)
	fillRoundedRect(img, x+2, y+2, w-4, h/2, r-2, color.RGBA{255, 255, 255, 24})
}

func drawHourlyForecastCard(img *image.RGBA, faces weatherCardFaces, x, y, w, h int, data weatherCardData, kind string) {
	drawGlassCard(img, x, y, w, h, 42, color.RGBA{65, 128, 212, 184})
	slots := weatherForecastSlots(data, kind)
	colW := w / len(slots)
	for i, slot := range slots {
		cx := x + colW*i + colW/2
		drawCenteredText(img, faces.Body, cx, y+58, slot.Label, color.RGBA{255, 255, 255, 255})
		drawSmallWeatherIcon(img, cx, y+102, slot.Kind)
		drawCenteredText(img, faces.H2, cx, y+178, displayTemperature(slot.Temp), color.RGBA{255, 255, 255, 255})
	}
}

type weatherForecastSlot struct {
	Label string
	Temp  string
	Kind  string
	Rain  string
}

func weatherForecastSlots(data weatherCardData, kind string) []weatherForecastSlot {
	slots := []weatherForecastSlot{{
		Label: "现在",
		Temp:  data.Temperature,
		Kind:  kind,
		Rain:  data.Precip,
	}}
	for _, hour := range upcomingHourlyForecast(data.Hourly, data.UpdatedAt, 5) {
		if len(slots) >= 6 {
			break
		}
		label := displayHourLabel(hour.Time)
		if label == "" {
			continue
		}
		temp := hour.Temp
		if temp == "" {
			temp = data.Temperature
		}
		slots = append(slots, weatherForecastSlot{
			Label: label,
			Temp:  temp,
			Kind:  classifyConditionText(hour.Condition),
			Rain:  hour.Rain,
		})
	}
	return slots
}

func upcomingHourlyForecast(hours []weatherForecastHour, updatedAt string, limit int) []weatherForecastHour {
	if limit <= 0 || len(hours) == 0 {
		return nil
	}
	referenceTime, hasReferenceTime := weatherTimestamp(updatedAt)
	ref, hasRef := weatherReferenceMinute(updatedAt)
	out := []weatherForecastHour{}
	for _, hour := range hours {
		if forecastTime, ok := weatherTimestamp(hour.Time); ok && hasReferenceTime {
			if !forecastTime.After(referenceTime) {
				continue
			}
		} else if hasRef {
			minute, ok := weatherHourMinute(hour.Time)
			if ok && minute <= ref {
				continue
			}
		}
		out = append(out, hour)
		if len(out) >= limit {
			return out
		}
	}
	return out
}

func classifyConditionText(condition string) string {
	return weatherConditionDisplayFor(condition).kind
}

func displayHourLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, ok := weatherTimestamp(value); ok {
		return fmt.Sprintf("%d时", parsed.In(weatherCardLocation).Hour())
	}
	if strings.Contains(value, ":") {
		fields := strings.Fields(value)
		if len(fields) > 0 {
			if minute, ok := weatherHourMinute(fields[len(fields)-1]); ok {
				return fmt.Sprintf("%d时", minute/60)
			}
			return fields[len(fields)-1]
		}
		if minute, ok := weatherHourMinute(value); ok {
			return fmt.Sprintf("%d时", minute/60)
		}
		return value
	}
	if n, err := strconv.Atoi(value); err == nil {
		hour := n / 100
		return fmt.Sprintf("%d时", hour)
	}
	return value
}

func weatherReferenceMinute(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		now := weatherCardNow()
		return now.Hour()*60 + now.Minute(), true
	}
	if parsed, ok := weatherTimestamp(value); ok {
		local := parsed.In(weatherCardLocation)
		return local.Hour()*60 + local.Minute(), true
	}
	for _, layout := range []string{
		"15:04",
		"3:04 PM",
	} {
		if parsed, err := time.ParseInLocation(layout, value, weatherCardLocation); err == nil {
			return parsed.Hour()*60 + parsed.Minute(), true
		}
	}
	fields := strings.Fields(value)
	for i := len(fields) - 1; i >= 0; i-- {
		if minute, ok := weatherHourMinute(fields[i]); ok {
			return minute, true
		}
	}
	return 0, false
}

func weatherTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.In(weatherCardLocation), true
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02 3:04 PM",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	} {
		if parsed, err := time.ParseInLocation(layout, value, weatherCardLocation); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func weatherHourMinute(value string) (int, bool) {
	value = strings.TrimSpace(strings.Trim(value, ","))
	if value == "" {
		return 0, false
	}
	for _, layout := range []string{"15:04", "3:04 PM"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Hour()*60 + parsed.Minute(), true
		}
	}
	if n, err := strconv.Atoi(value); err == nil {
		hour := n / 100
		minute := n % 100
		if hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59 {
			return hour*60 + minute, true
		}
	}
	return 0, false
}

func drawSmallWeatherIcon(img *image.RGBA, cx, cy int, kind string) {
	switch kind {
	case "unknown":
		drawCircle(img, cx, cy, 24, color.RGBA{222, 232, 244, 240})
		drawCenteredText(img, basicfont.Face7x13, cx, cy+5, "?", color.RGBA{73, 100, 132, 255})
	case "rain":
		drawCircle(img, cx-10, cy, 16, color.RGBA{255, 255, 255, 245})
		drawCircle(img, cx+8, cy-10, 21, color.RGBA{255, 255, 255, 245})
		drawCircle(img, cx+26, cy+2, 15, color.RGBA{255, 255, 255, 245})
		drawThickLine(img, cx-9, cy+25, cx-17, cy+43, 4, color.RGBA{230, 242, 255, 220})
		drawThickLine(img, cx+14, cy+25, cx+6, cy+43, 4, color.RGBA{230, 242, 255, 220})
	case "clear":
		drawCircle(img, cx, cy, 21, color.RGBA{255, 201, 32, 255})
		for i := 0; i < 8; i++ {
			a := float64(i) * math.Pi / 4
			drawThickLine(img, cx+int(math.Cos(a)*29), cy+int(math.Sin(a)*29), cx+int(math.Cos(a)*40), cy+int(math.Sin(a)*40), 4, color.RGBA{255, 201, 32, 230})
		}
	default:
		drawCircle(img, cx+18, cy-14, 18, color.RGBA{255, 198, 24, 255})
		drawCircle(img, cx-14, cy+3, 17, color.RGBA{255, 255, 255, 245})
		drawCircle(img, cx+6, cy-5, 23, color.RGBA{255, 255, 255, 245})
		drawCircle(img, cx+27, cy+4, 17, color.RGBA{255, 255, 255, 245})
		fillRoundedRect(img, cx-28, cy+7, 64, 21, 11, color.RGBA{255, 255, 255, 245})
	}
}

func drawCenteredText(img *image.RGBA, face font.Face, cx, y int, text string, c color.Color) {
	drawText(img, face, cx-textAdvance(face, text)/2, y, text, c)
}

func firstWeatherCardFont(paths []string) (*opentype.Font, error) {
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			continue
		}
		if parsed, err := parseWeatherCardFont(raw); err == nil && weatherFontSupportsChinese(parsed) {
			return parsed, nil
		}
	}
	return nil, errors.New("no readable Chinese weather card font")
}

func parseWeatherCardFont(raw []byte) (*opentype.Font, error) {
	if parsed, err := opentype.Parse(raw); err == nil && weatherFontSupportsChinese(parsed) {
		return parsed, nil
	}
	collection, err := opentype.ParseCollection(raw)
	if err != nil {
		return nil, err
	}
	var fallback *opentype.Font
	for i := 0; i < collection.NumFonts(); i++ {
		font, err := collection.Font(i)
		if err != nil || !weatherFontSupportsChinese(font) {
			continue
		}
		if weatherFontIsSimplifiedChinese(font) {
			return font, nil
		}
		if fallback == nil {
			fallback = font
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, errors.New("no usable font in collection")
}

func weatherFontIsSimplifiedChinese(font *opentype.Font) bool {
	var buf sfnt.Buffer
	for _, id := range []sfnt.NameID{sfnt.NameIDFamily, sfnt.NameIDFull, sfnt.NameIDPostScript} {
		name, err := font.Name(&buf, id)
		if err != nil {
			continue
		}
		name = strings.ToLower(name)
		if strings.Contains(name, "cjk sc") || strings.Contains(name, "pingfang sc") ||
			strings.Contains(name, "simplified chinese") || strings.Contains(name, "gb") {
			return true
		}
	}
	return false
}

func weatherFontSupportsChinese(font *opentype.Font) bool {
	if font == nil {
		return false
	}
	var buf sfnt.Buffer
	for _, r := range []rune("天气温馨提示杭州多云雨") {
		index, err := font.GlyphIndex(&buf, r)
		if err != nil || index == 0 {
			return false
		}
	}
	return true
}

func drawInfoCard(img *image.RGBA, faces weatherCardFaces, x, y, w, h int, bg color.RGBA, title, body string, accent, text color.Color) {
	fillRoundedRect(img, x, y, w, h, 48, bg)
	drawText(img, faces.H2, x+76, y+128, title, accent)
	lines := splitCardLines(body, 18)
	for i, line := range lines {
		if i >= 3 {
			break
		}
		drawText(img, faces.Body, x+80, y+214+i*66, line, text)
	}
}

func drawWeatherHeroIcon(img *image.RGBA, x, y, size int, kind string) {
	switch kind {
	case "rain":
		drawCloudIcon(img, x+70, y+106, size, true, false)
	case "snow":
		drawCloudIcon(img, x+70, y+106, size, false, true)
	case "clear":
		drawSunIcon(img, x+20, y+18, 188)
	default:
		drawSunIcon(img, x+12, y+10, 170)
		drawCloudIcon(img, x+98, y+118, size, false, false)
	}
}

func drawSunIcon(img *image.RGBA, cx, cy, radius int) {
	yellow := color.RGBA{255, 193, 24, 255}
	drawCircle(img, cx+radius, cy+radius, radius, yellow)
	for i := 0; i < 10; i++ {
		angle := float64(i) * math.Pi / 5
		x1 := cx + radius + int(math.Cos(angle)*float64(radius+32))
		y1 := cy + radius + int(math.Sin(angle)*float64(radius+32))
		x2 := cx + radius + int(math.Cos(angle)*float64(radius+76))
		y2 := cy + radius + int(math.Sin(angle)*float64(radius+76))
		drawThickLine(img, x1, y1, x2, y2, 18, yellow)
	}
}

func drawCloudIcon(img *image.RGBA, x, y, size int, rain, snow bool) {
	cloud := color.RGBA{224, 239, 252, 255}
	shadow := color.RGBA{205, 226, 246, 255}
	drawCircle(img, x+116, y+138, 92, cloud)
	drawCircle(img, x+234, y+98, 124, cloud)
	drawCircle(img, x+358, y+148, 106, cloud)
	fillRoundedRect(img, x+80, y+150, size-20, 116, 56, cloud)
	fillRoundedRect(img, x+122, y+242, size-100, 24, 12, shadow)
	drop := color.RGBA{0, 122, 45, 255}
	for i := 0; i < 4; i++ {
		dx := x + 132 + i*82
		if rain {
			drawThickLine(img, dx, y+306, dx-18, y+360, 10, drop)
		}
		if snow {
			drawText(img, basicfont.Face7x13, dx, y+340, "*", drop)
		}
	}
}

func drawHatIcon(img *image.RGBA, cx, cy int, c color.Color) {
	drawThickLine(img, cx-80, cy+40, cx+86, cy+40, 10, c)
	drawThickLine(img, cx-104, cy+24, cx-80, cy+40, 10, c)
	drawThickLine(img, cx+86, cy+40, cx+112, cy+20, 10, c)
	drawArc(img, cx, cy+4, 72, math.Pi, 2*math.Pi, 10, c)
	drawThickLine(img, cx-72, cy+4, cx+72, cy+4, 10, c)
	drawThickLine(img, cx-58, cy-22, cx+56, cy+6, 9, c)
}

func drawUmbrellaIcon(img *image.RGBA, cx, cy int, c color.Color) {
	drawArc(img, cx, cy+8, 82, math.Pi, 2*math.Pi, 10, c)
	drawThickLine(img, cx-82, cy+8, cx+82, cy+8, 8, c)
	drawThickLine(img, cx, cy+8, cx, cy+110, 10, c)
	drawArc(img, cx+24, cy+108, 28, 0, math.Pi, 8, c)
	for i := -2; i <= 2; i++ {
		drawCircle(img, cx+i*42, cy-64-int(math.Abs(float64(i))*10), 9, c)
		drawThickLine(img, cx+i*42, cy-52-int(math.Abs(float64(i))*10), cx+i*42-10, cy-26, 7, c)
	}
}

func fillRoundedRect(img *image.RGBA, x, y, w, h, r int, c color.RGBA) {
	for py := y; py < y+h; py++ {
		for px := x; px < x+w; px++ {
			dx := 0
			if px < x+r {
				dx = x + r - px
			} else if px >= x+w-r {
				dx = px - (x + w - r - 1)
			}
			dy := 0
			if py < y+r {
				dy = y + r - py
			} else if py >= y+h-r {
				dy = py - (y + h - r - 1)
			}
			if dx > 0 && dy > 0 && dx*dx+dy*dy > r*r {
				continue
			}
			blendRGBA(img, px, py, c)
		}
	}
}

func drawCircle(img *image.RGBA, cx, cy, r int, c color.Color) {
	rgba := color.RGBAModel.Convert(c).(color.RGBA)
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r*r {
				blendRGBA(img, x, y, rgba)
			}
		}
	}
}

func drawArc(img *image.RGBA, cx, cy, r int, start, end float64, width int, c color.Color) {
	rgba := color.RGBAModel.Convert(c).(color.RGBA)
	for a := start; a <= end; a += 0.01 {
		x := cx + int(math.Cos(a)*float64(r))
		y := cy + int(math.Sin(a)*float64(r))
		drawCircle(img, x, y, width/2, rgba)
	}
}

func drawThickLine(img *image.RGBA, x1, y1, x2, y2, width int, c color.Color) {
	rgba := color.RGBAModel.Convert(c).(color.RGBA)
	steps := int(math.Hypot(float64(x2-x1), float64(y2-y1)))
	if steps <= 0 {
		drawCircle(img, x1, y1, width/2, rgba)
		return
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := int(float64(x1)*(1-t) + float64(x2)*t)
		y := int(float64(y1)*(1-t) + float64(y2)*t)
		drawCircle(img, x, y, width/2, rgba)
	}
}

func drawClockIcon(img *image.RGBA, cx, cy int, c color.Color) {
	drawArc(img, cx, cy, 24, 0, 2*math.Pi, 8, c)
	drawThickLine(img, cx, cy, cx, cy-14, 6, c)
	drawThickLine(img, cx, cy, cx+12, cy+8, 6, c)
}

func blendRGBA(img *image.RGBA, x, y int, src color.RGBA) {
	if !image.Pt(x, y).In(img.Bounds()) {
		return
	}
	if src.A == 255 {
		img.SetRGBA(x, y, src)
		return
	}
	dst := img.RGBAAt(x, y)
	a := float64(src.A) / 255
	img.SetRGBA(x, y, color.RGBA{
		R: uint8(float64(src.R)*a + float64(dst.R)*(1-a)),
		G: uint8(float64(src.G)*a + float64(dst.G)*(1-a)),
		B: uint8(float64(src.B)*a + float64(dst.B)*(1-a)),
		A: 255,
	})
}

func drawVerticalGradient(img *image.RGBA, top, bottom color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		t := float64(y-b.Min.Y) / float64(b.Dy())
		c := color.RGBA{
			R: uint8(float64(top.R)*(1-t) + float64(bottom.R)*t),
			G: uint8(float64(top.G)*(1-t) + float64(bottom.G)*t),
			B: uint8(float64(top.B)*(1-t) + float64(bottom.B)*t),
			A: 255,
		}
		for x := b.Min.X; x < b.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func drawRoundedLikeRect(img *image.RGBA, x, y, w, h int, c color.Color) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), &image.Uniform{c}, image.Point{}, draw.Over)
}

func drawPill(img *image.RGBA, face font.Face, x, y int, text string, c color.Color) {
	draw.Draw(img, image.Rect(x, y-36, x+320, y+20), &image.Uniform{color.RGBA{236, 253, 245, 255}}, image.Point{}, draw.Over)
	drawText(img, face, x+20, y, text, c)
}

func drawText(img *image.RGBA, face font.Face, x, y int, text string, c color.Color) {
	if strings.TrimSpace(text) == "" {
		return
	}
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(text)
}

func drawTemperature(img *image.RGBA, faces weatherCardFaces, x, y int, value string, c color.Color) {
	number, unit := splitTemperatureDisplay(value)
	drawText(img, faces.Hero, x, y, number, c)
	advance := textAdvance(faces.Hero, number)
	drawText(img, faces.H2, x+advance+6, y-86, unit, c)
}

func splitTemperatureDisplay(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "--") {
		return "--", "°"
	}
	if n, ok := weatherNumber(value); ok {
		return fmt.Sprintf("%.0f", n), "°"
	}
	value = strings.NewReplacer(
		"℃", "",
		"°C", "",
		"°c", "",
		"° C", "",
		"° c", "",
		"摄氏度", "",
		"摄氏", "",
		"celsius", "",
		"Celsius", "",
		"°", "",
	).Replace(value)
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, "Cc")
	value = strings.TrimSpace(value)
	if value == "" {
		return "--", "°"
	}
	return value, "°"
}

func textAdvance(face font.Face, text string) int {
	if face == nil || text == "" {
		return 0
	}
	d := &font.Drawer{Face: face}
	return (d.MeasureString(text) + fixed.I(1)/2).Round()
}

func wrapRunes(text string, limit int) []string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	runes := []rune(text)
	lines := []string{}
	for len(runes) > 0 {
		n := limit
		if len(runes) < n {
			n = len(runes)
		}
		lines = append(lines, string(runes[:n]))
		runes = runes[n:]
	}
	return lines
}

func splitCardLines(text string, limit int) []string {
	out := []string{}
	for _, part := range strings.Split(text, "\n") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, wrapRunes(part, limit)...)
	}
	if len(out) == 0 {
		return []string{"暂无明确提示"}
	}
	return out
}

func displayTemperature(value string) string {
	n, ok := weatherNumber(value)
	if ok {
		return fmt.Sprintf("%.0f°", n)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "--°"
	}
	value = strings.ReplaceAll(value, "°c", "°C")
	if !strings.Contains(value, "°") {
		value += "°"
	}
	return value
}

// weatherConditionDisplays maps every value of the closed contract condition
// set to its Chinese card label and the icon/advice kind consumed by the
// drawing and tip helpers. TestWeatherConditionDisplayCoversContract keeps it
// exhaustive over infinimeshinfo.AllWeatherConditions.
type weatherConditionDisplay struct {
	label string
	kind  string
}

var weatherConditionDisplays = map[infinimeshinfo.WeatherCondition]weatherConditionDisplay{
	infinimeshinfo.WeatherConditionClear:        {label: "晴", kind: "clear"},
	infinimeshinfo.WeatherConditionPartlyCloudy: {label: "多云", kind: "partly"},
	infinimeshinfo.WeatherConditionCloudy:       {label: "阴", kind: "partly"},
	infinimeshinfo.WeatherConditionHaze:         {label: "霾", kind: "partly"},
	infinimeshinfo.WeatherConditionFog:          {label: "雾", kind: "partly"},
	infinimeshinfo.WeatherConditionDust:         {label: "浮尘", kind: "partly"},
	infinimeshinfo.WeatherConditionSand:         {label: "沙尘", kind: "partly"},
	infinimeshinfo.WeatherConditionWind:         {label: "大风", kind: "partly"},
	infinimeshinfo.WeatherConditionLightRain:    {label: "小雨", kind: "rain"},
	infinimeshinfo.WeatherConditionModerateRain: {label: "中雨", kind: "rain"},
	infinimeshinfo.WeatherConditionHeavyRain:    {label: "大雨", kind: "rain"},
	infinimeshinfo.WeatherConditionStormRain:    {label: "暴雨", kind: "rain"},
	infinimeshinfo.WeatherConditionLightSnow:    {label: "小雪", kind: "snow"},
	infinimeshinfo.WeatherConditionModerateSnow: {label: "中雪", kind: "snow"},
	infinimeshinfo.WeatherConditionHeavySnow:    {label: "大雪", kind: "snow"},
	infinimeshinfo.WeatherConditionStormSnow:    {label: "暴雪", kind: "snow"},
	infinimeshinfo.WeatherConditionUnknown:      {label: "暂无数据", kind: "unknown"},
}

func weatherConditionDisplayFor(condition string) weatherConditionDisplay {
	if display, ok := weatherConditionDisplays[infinimeshinfo.WeatherCondition(strings.TrimSpace(condition))]; ok {
		return display
	}
	return weatherConditionDisplays[infinimeshinfo.WeatherConditionUnknown]
}

func displayCondition(condition string) string {
	return weatherConditionDisplayFor(condition).label
}

func displayUpdateTime(value string) string {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		"2006-01-02 15:04",
		"2006-01-02 3:04 PM",
		"15:04",
		"3:04 PM",
	} {
		if parsed, err := time.ParseInLocation(layout, value, weatherCardLocation); err == nil {
			return parsed.Format("15:04")
		}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.In(weatherCardLocation).Format("15:04")
	}
	if value == "" {
		return weatherCardNow().Format("15:04")
	}
	fields := strings.Fields(value)
	if len(fields) > 0 {
		last := strings.Trim(fields[len(fields)-1], ",")
		if len(last) >= 4 && strings.Contains(last, ":") {
			return last
		}
	}
	return value
}

func weatherCardNow() time.Time {
	return time.Now().In(weatherCardLocation)
}

func mustWeatherCardLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return loc
}

func classifyWeatherKind(data weatherCardData) string {
	return weatherConditionDisplayFor(data.Condition).kind
}

func weatherAlert(data weatherCardData, kind string) (string, string) {
	if data.MissingData {
		return "数据暂缺", "Info 未返回完整实时数据，\n卡片仅展示已有结果"
	}
	temp, hasTemp := weatherNumber(data.Temperature)
	feels, hasFeels := weatherNumber(data.FeelsLike)
	hot := (hasTemp && temp >= 32) || (hasFeels && feels >= 34)
	cold := (hasTemp && temp <= 3) || (hasFeels && feels <= 0)
	uv, hasUV := weatherNumber(data.UV)
	wind := strings.TrimSpace(data.Wind)
	switch {
	case hot || (hasUV && uv >= 6):
		return "注意避暑", "午后紫外线较强，\n减少长时间户外活动"
	case cold:
		return "注意保暖", "体感温度偏低，\n外出注意添衣防风"
	case strings.Contains(wind, "km/h") && windNumberKmh(wind) >= 28:
		return "注意大风", "户外风感明显，\n留意高空坠物和扬尘"
	case kind == "rain":
		return "注意降雨", "局地可能有降雨，\n出行留意路面积水"
	case kind == "snow":
		return "注意降雪", "道路可能湿滑，\n出行预留更多时间"
	default:
		return "天气提醒", "关注临近预报，\n按体感增减衣物"
	}
}

func weatherReminder(data weatherCardData, kind string) (string, string) {
	rainLikely := kind == "rain" || maxForecastRain(data.Forecast) >= 50 || precipMM(data.Precip) >= 1
	temp, hasTemp := weatherNumber(data.Temperature)
	switch {
	case rainLikely:
		return "今日提醒", "傍晚可能有阵雨，\n出门带伞"
	case kind == "snow":
		return "今日提醒", "可能出现降雪，\n注意防滑保暖"
	case hasTemp && temp >= 30:
		return "今日提醒", "午后体感偏热，\n及时补水防晒"
	case hasTemp && temp <= 5:
		return "今日提醒", "早晚温差明显，\n外套随身带好"
	default:
		return "今日提醒", "天气总体平稳，\n出门前再看实时预报"
	}
}

func weatherRainWindow(data weatherCardData, kind string) (string, string) {
	currentRain := precipMM(data.Precip)
	forecastRain := maxForecastRain(data.Forecast)
	switch {
	case kind == "rain" || currentRain >= 1 || forecastRain >= 70:
		return "未来几小时有雨", "降雨概率较高，\n外出建议带伞"
	case currentRain > 0 || forecastRain >= 40:
		return "可能有零星降雨", "短时可能飘雨，\n出门可备折叠伞"
	default:
		return "未来几小时少雨", "暂无明显降雨信号，\n仍建议留意临近预报"
	}
}

func weatherWarmTip(data weatherCardData, kind string) (string, string) {
	if data.MissingData {
		return "数据说明", "部分实时数据暂缺，\n未使用推测值补全"
	}
	temp, hasTemp := weatherNumber(data.Temperature)
	feels, hasFeels := weatherNumber(data.FeelsLike)
	uv, hasUV := weatherNumber(data.UV)
	wind := windNumberKmh(data.Wind)
	switch {
	case (hasTemp && temp >= 32) || (hasFeels && feels >= 34) || (hasUV && uv >= 6):
		return "温馨提示", "午后体感偏热，\n注意补水防晒"
	case hasTemp && temp <= 5:
		return "温馨提示", "早晚温度较低，\n注意添衣保暖"
	case wind >= 28:
		return "温馨提示", "风感比较明显，\n户外注意防风"
	case kind == "snow":
		return "温馨提示", "道路可能湿滑，\n出行注意防滑"
	case kind == "rain":
		return "温馨提示", "路面可能湿滑，\n通勤预留时间"
	default:
		return "温馨提示", "天气总体平稳，\n按体感增减衣物"
	}
}

func maxForecastRain(days []weatherForecastDay) float64 {
	var max float64
	for _, day := range days {
		if n, ok := weatherNumber(day.Rain); ok && n > max {
			max = n
		}
	}
	return max
}

func precipMM(value string) float64 {
	n, _ := weatherNumber(value)
	return n
}

func windNumberKmh(value string) float64 {
	n, _ := weatherNumber(value)
	return n
}

func weatherNumber(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= '0' && r <= '9') || r == '-' || r == '.' {
			b.WriteRune(r)
			continue
		}
		if b.Len() > 0 {
			break
		}
	}
	if b.Len() == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(b.String(), 64)
	return n, err == nil
}

func weatherSuggestion(data weatherCardData) string {
	if data.MissingData {
		return "部分实时天气数据暂缺，卡片仅显示 Info 返回的内容。"
	}
	switch classifyWeatherKind(data) {
	case "rain":
		return "有降雨可能，出门建议带伞并留意路面湿滑。"
	case "snow":
		return "有降雪可能，注意保暖和交通延误。"
	case "clear":
		return "天气较好，注意补水和防晒。"
	default:
		return "出门前再确认一次实时天气，按体感增减衣物。"
	}
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

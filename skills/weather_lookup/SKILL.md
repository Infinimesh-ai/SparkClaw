---
name: weather_lookup
description: Render a clear weather card from Open-Meteo weather data by default.
risk_level: low
input_schema:
  type: object
  properties:
    location:
      type: string
    question:
      type: string
  required: [question]
dependencies:
  - media.render_weather_card
eval_cases:
  - weather_lookup_current
  - weather_lookup_forecast
allowed_tools:
  - media.render_weather_card
denied_tools:
  - browser.read
  - web.search
  - browser.submit_form
  - files.write_draft
  - shell.exec_sandboxed
  - code.apply_patch
activation:
  keywords: ["weather", "forecast", "temperature", "rain", "snow", "wind", "umbrella", "天气", "气温", "温度", "预报", "下雨", "下雪", "刮风", "带伞", "空气质量"]
---

Use this skill when the user asks about weather, forecast, temperature, rain, snow, wind, or whether they should bring an umbrella.

Hard requirements:
- Default weather answers must call `media.render_weather_card` with the current message location. Do not use memory, prior weather data, previous tool observations, guessed JSON, `browser.read`, or old artifact/snapshot refs as the weather source.
- `media.render_weather_card` owns weather lookup, parsing, hourly forecast extraction, and image rendering. The model only supplies the location and then returns the generated image.
- Do not invent, smooth, correct, or substitute temperature/weather fields. The tool only uses Open-Meteo data. If Open-Meteo lookup fails, return the tool error clearly instead of using another weather source.

Workflow:
1. Determine the location from the current user message. If missing, use recent conversation context when it clearly gives the location. If no location is available, ask a short clarification question.
2. Call `media.render_weather_card` directly with `{"location":"<location>"}`. Do not pass `raw_json`, `snapshot_ref`, `raw_json_ref`, historical artifact references, browser snapshots, or hand-written weather fields.
3. The rendered card should include current weather and the next few hours when available. The bottom hourly row only needs hour label, condition, and temperature.
4. Open-Meteo is the only weather source inside `media.render_weather_card`; do not use any legacy weather website or other fallback source.
5. When the weather card is generated successfully, the user-visible final answer should contain only that one image. Do not add extra text, path, success wording, repeated weather summary, or source/status details outside the image.
6. If the user is on Weixin/vx/mobile chat, or asks to send the weather image, return a single final Markdown image link using the returned `media_path`. Runtime does not choose recipients or call channel delivery.
7. Use concise Chinese text only as a fallback: when the user explicitly asks for plain text/no image/no card, or when card rendering fails. Mention location, current condition, temperature/feels-like, rain or snow risk, wind if relevant, and a practical suggestion.
8. If card rendering fails, answer briefly with the actual tool error, for example that Open-Meteo lookup failed for the requested location. Do not retry with stale data or another weather website.
9. For severe weather, disaster warnings, school/work closure decisions, flights, or other high-stakes situations, advise checking the official meteorological authority or local emergency source.

Do not use `web.search` or `browser.read` for ordinary weather lookup. Use search only if the user explicitly asks for official weather warnings, news, or a source outside Open-Meteo.

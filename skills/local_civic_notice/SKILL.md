---
name: local_civic_notice
description: Check local public-service notices such as water, power, gas, heating, and municipal service interruptions, prioritizing official sources.
risk_level: low
input_schema:
  type: object
  properties:
    location:
      type: string
    date:
      type: string
    issue:
      type: string
    question:
      type: string
  required: [question]
dependencies:
  - web.search
  - browser.read
  - official_source_priority
eval_cases:
  - local_water_outage_official_source
  - local_power_outage_official_source
  - local_civic_notice_insufficient_evidence
allowed_tools:
  - web.search
  - browser.read
denied_tools:
  - browser.submit_form
  - files.write_draft
  - shell.exec_sandboxed
  - code.apply_patch
activation:
  keywords: ["停水", "停电", "停气", "停暖", "小区", "街道", "社区", "供水", "自来水", "水务", "电力", "燃气", "热力", "断水", "断电", "outage", "water outage", "power outage"]
---

Use this skill when the user asks whether a local area, district, community, street, or compound will have a water outage, power outage, gas outage, heating interruption, service interruption, or similar local public-service notice.

Workflow:
1. First identify the required facts: city, district/county, date, issue type, and specific address/community/compound. If city, district/county, date, or issue type is missing, ask a short clarification question. If only the exact community/address is missing, you may search district-level public notices but must warn that community-level confirmation needs a specific address.
2. Resolve relative dates against the temporal context. For example, "明天" means the date after `local_date`. Always verify the year in search queries, source titles, snippets, and final answer.
3. Search with the location, resolved date, issue type, and likely official operator or regulator.
4. Prioritize official sources:
   - government websites;
   - water/electricity/gas/heating company websites;
   - official public-account or customer-service pages when visible in search/fetch results;
   - municipal service hotline pages.
5. Use authoritative media reposts only after official sources. Third-party platforms are clues only and must not be the final basis. Sites such as 本地宝, 搜狐, 新浪, 今日头条, 百度聚合, random portals, and forum posts can help discover leads but cannot confirm the outage.
6. Use `browser.read` on official-looking result pages before relying on them. Do not treat a third-party list page as confirmation.
7. If there is no official source that clearly states the exact date and relevant location, final answer must say that no public official notice was found and that the outage cannot be confirmed. Do not say "肯定没有", "一定不停", or "不会停" from absence of search results.
8. For community-level, compound-level, school, park, office, dormitory, or unit-specific matters, advise checking property management, logistics/后勤, community notices, the operator hotline, or municipal service hotline because internal maintenance may not appear in city-level notices.

Never invent a yes/no outage answer from weak evidence. "No public official notice found" is not the same as "definitely no outage".

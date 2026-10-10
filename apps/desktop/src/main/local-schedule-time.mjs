// Temporal input belongs to the originating workbench's clock. Task content is
// opaque here; execution and business interpretation still belong to Gateway.
const DAY = 86400000;
const formatters = new Map();
export function dateParts(time, timezone) {
  let format = formatters.get(timezone);
  if (!format) {
    format = new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' });
    formatters.set(timezone, format);
  }
  return Object.fromEntries(format.formatToParts(time).filter(part => part.type !== 'literal').map(part => [part.type, Number(part.value)]));
}
export function zonedTime(value, timezone) {
  const match = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})(?::(\d{2}))?$/.exec(value);
  if (!match) throw new Error('Specify a date and time, for example 2026-10-11 09:00.');
  const [year, month, day, hour, minute, second] = match.slice(1).map(Number);
  const wall = Date.UTC(year, month - 1, day, hour, minute, second || 0);
  let instant = wall;
  for (let index = 0; index < 4; index++) {
    const parts = dateParts(instant, timezone);
    const displayed = Date.UTC(parts.year, parts.month - 1, parts.day, parts.hour, parts.minute, parts.second);
    if (displayed === wall) {
      if (new Date(wall).getUTCMonth() + 1 !== month || hour > 23 || minute > 59 || (second || 0) > 59) break;
      return new Date(instant).toISOString();
    }
    instant += wall - displayed;
  }
  throw new Error('This local date or time does not exist in the selected timezone.');
}
const wallString = parts => `${parts.year}-${String(parts.month).padStart(2, '0')}-${String(parts.day).padStart(2, '0')}T${String(parts.hour).padStart(2, '0')}:${String(parts.minute).padStart(2, '0')}:${String(parts.second || 0).padStart(2, '0')}`;
function shiftDate(parts, days) {
  const date = new Date(Date.UTC(parts.year, parts.month - 1, parts.day + days));
  return { ...parts, year: date.getUTCFullYear(), month: date.getUTCMonth() + 1, day: date.getUTCDate() };
}
export function nextCalendarTime(dueAt, calendar, count = 1) {
  let parts = dateParts(Date.parse(dueAt), calendar.timezone);
  parts = { ...parts, hour: calendar.hour ?? parts.hour, minute: calendar.minute ?? parts.minute, second: calendar.second ?? parts.second };
  if (calendar.kind === 'monthly') {
    const first = new Date(Date.UTC(parts.year, parts.month - 1 + count, 1));
    const last = new Date(Date.UTC(first.getUTCFullYear(), first.getUTCMonth() + 1, 0)).getUTCDate();
    parts = { ...parts, year: first.getUTCFullYear(), month: first.getUTCMonth() + 1, day: Math.min(calendar.day, last) };
  } else if (calendar.kind === 'weekdays') {
    const weekday = new Date(Date.UTC(parts.year, parts.month - 1, parts.day)).getUTCDay();
    if (weekday === 0 || weekday === 6) { parts = shiftDate(parts, weekday === 0 ? 1 : 2); count--; }
    parts = shiftDate(parts, Math.floor(count / 5) * 7);
    for (let remainder = count % 5; remainder > 0;) {
      parts = shiftDate(parts, 1);
      const weekday = new Date(Date.UTC(parts.year, parts.month - 1, parts.day)).getUTCDay();
      if (weekday !== 0 && weekday !== 6) remainder--;
    }
  } else parts = shiftDate(parts, count * (calendar.kind === 'weekly' ? 7 : 1));
  // Keep the requested wall time across daylight-saving gaps, advancing to the
  // first existing minute on that date rather than losing the whole definition.
  for (let minutes = 0; minutes < 181; minutes++) {
    try { return zonedTime(wallString(parts), calendar.timezone); }
    catch {
      const date = new Date(Date.UTC(parts.year, parts.month - 1, parts.day, parts.hour, parts.minute + 1));
      parts = { ...parts, hour: date.getUTCHours(), minute: date.getUTCMinutes() };
    }
  }
  throw new Error('The next scheduled time is unavailable.');
}
export function calendarMissed(dueAt, calendar, now) {
  const start = dateParts(Date.parse(dueAt), calendar.timezone), end = dateParts(now, calendar.timezone);
  let low = 0, high = calendar.kind === 'monthly' ? (end.year - start.year) * 12 + end.month - start.month + 2 : Math.ceil((now - Date.parse(dueAt)) / DAY) + 2;
  while (low + 1 < high) {
    const middle = Math.floor((low + high) / 2);
    if (Date.parse(nextCalendarTime(dueAt, calendar, middle)) <= now) low = middle;
    else high = middle;
  }
  return { count: low + 1, lastDue: low ? nextCalendarTime(dueAt, calendar, low) : dueAt, nextDue: nextCalendarTime(dueAt, calendar, low + 1) };
}
function number(value) {
  if (/^\d+$/.test(value)) return Number(value);
  const digits = { 零: 0, 一: 1, 二: 2, 两: 2, 三: 3, 四: 4, 五: 5, 六: 6, 七: 7, 八: 8, 九: 9 };
  if (value.includes('十')) { const [tens, ones] = value.split('十'); return (tens ? digits[tens] : 1) * 10 + (ones ? digits[ones] : 0); }
  return digits[value];
}
export function recurrenceSpec(value, timezone, dueAt) {
  const recurrence = value?.trim().toLowerCase() || 'none';
  dateParts(Date.parse(dueAt), timezone);
  if (['none', 'once', '单次', '仅一次', ''].includes(recurrence)) return { intervalMS: 0, calendar: {} };
  const kind = ({ daily: 'daily', 每天: 'daily', weekly: 'weekly', 每周: 'weekly', monthly: 'monthly', 每月: 'monthly', weekdays: 'weekdays', 工作日: 'weekdays' })[recurrence];
  if (kind) {
    const parts = dateParts(Date.parse(dueAt), timezone);
    return { intervalMS: 0, calendar: { kind, timezone, hour: parts.hour, minute: parts.minute, second: parts.second, ...(kind === 'monthly' ? { day: parts.day } : {}) } };
  }
  const duration = /^(?:every\s*|每(?:隔)?\s*)(\d+|[一二两三四五六七八九十]+)\s*(?:个\s*)?(minutes?|hours?|days?|weeks?|分钟|小时|天|周)$/i.exec(recurrence);
  const milliseconds = { minute: 60000, minutes: 60000, 分钟: 60000, hour: 3600000, hours: 3600000, 小时: 3600000, day: DAY, days: DAY, 天: DAY, week: 7 * DAY, weeks: 7 * DAY, 周: 7 * DAY };
  if (!duration) throw new Error('Specify none, daily, weekly, monthly, weekdays, or an interval such as every 1 hour.');
  return { intervalMS: number(duration[1]) * milliseconds[duration[2].toLowerCase()], calendar: {} };
}
export function parseScheduleRequest(request, timezone, now) {
  if (typeof request !== 'string' || !request.trim() || Buffer.byteLength(request) > 65536) throw new Error('Schedule request is invalid.');
  request = request.trim();
  const current = dateParts(now, timezone);
  let parts = { ...current, second: 0 }, recurrence = 'none', intervalMS = 0, monthlyDay;
  const spans = [];
  const header = request.split(/[,，。;；\n]/, 1)[0];
  const clockPattern = /(?:早上|上午|中午|下午|晚上|今晚|凌晨|at\s*)?\s*(\d{1,2}|[一二两三四五六七八九十]+)(?:\s*[:：]\s*(\d{2})|\s*点(?:\s*(半|\d{1,2})\s*分?)?|\s*(AM|PM))\s*(AM|PM)?/i;
  const firstClock = clockPattern.exec(header);
  const take = match => {
    if (match && firstClock && match.index > firstClock.index && header.slice(firstClock.index + firstClock[0].length, match.index).trim()) return null;
    if (match) spans.push([match.index, match.index + match[0].length]); return match;
  };
  const repeated = take(/每(?:个)?工作日|every weekday|weekdays|每天|每日|every day|daily|每周[一二三四五六日天]?|every (?:monday|tuesday|wednesday|thursday|friday|saturday|sunday)|weekly|每月(?:\s*\d+\s*[日号])?|monthly/i.exec(header));
  const interval = repeated ? null : take(/(?:每(?:隔)?\s*|every\s+)(\d+|[一二两三四五六七八九十]+)\s*(?:个\s*)?(分钟|小时|天|周|minutes?|hours?|days?|weeks?)/i.exec(header));
  if (repeated) {
    const phrase = repeated[0].toLowerCase();
    recurrence = /工作日|weekday/.test(phrase) ? 'weekdays' : /每月|monthly/.test(phrase) ? 'monthly' : /每周|weekly|monday|tuesday|wednesday|thursday|friday|saturday|sunday/.test(phrase) ? 'weekly' : 'daily';
    if (recurrence === 'monthly') { const day = /\d+/.exec(phrase); if (day) {
      monthlyDay = Number(day[0]); if (monthlyDay < 1 || monthlyDay > 31) throw new Error('Monthly day must be between 1 and 31.');
      parts.day = Math.min(monthlyDay, new Date(Date.UTC(parts.year, parts.month, 0)).getUTCDate());
    } }
    if (recurrence === 'weekly') {
      const weekdays = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday'];
      const zh = { 一: 1, 二: 2, 三: 3, 四: 4, 五: 5, 六: 6, 日: 0, 天: 0 };
      const target = phrase.startsWith('每周') ? zh[phrase.at(-1)] : weekdays.findIndex(day => phrase.includes(day));
      if (target !== undefined && target >= 0) parts = shiftDate(parts, (target - new Date(Date.UTC(parts.year, parts.month - 1, parts.day)).getUTCDay() + 7) % 7);
    }
  } else if (interval) intervalMS = recurrenceSpec(interval[0], timezone, new Date(now).toISOString()).intervalMS;
  const relative = interval ? null : take(/in\s+(\d+)\s*(minutes?|hours?|days?|weeks?)|(\d+|[一二两三四五六七八九十]+)\s*(?:个\s*)?(分钟|小时|天|周|minutes?|hours?|days?|weeks?)\s*(?:后|later|from now)/i.exec(header));
  const absolute = take(/\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2})?)?/.exec(header));
  const day = take(/后天|明天|今天|tomorrow|today/i.exec(header));
  const clock = take(firstClock);
  let dueAt;
  if (absolute) {
    const [date, time] = absolute[0].split(/[T ]/);
    const [year, month, dateDay] = date.split('-').map(Number);
    parts = { ...parts, year, month, day: dateDay };
    if (time) { const [hour, minute, second = 0] = time.split(':').map(Number); parts = { ...parts, hour, minute, second }; }
    else if (!clock) throw new Error('请补充执行时间，例如明天早上 9 点。 Specify an execution time, such as tomorrow at 9 AM.');
  } else if (relative && !interval) {
    const units = { 分钟: 60000, 小时: 3600000, 天: DAY, 周: 7 * DAY, minute: 60000, minutes: 60000, hour: 3600000, hours: 3600000, day: DAY, days: DAY, week: 7 * DAY, weeks: 7 * DAY };
    dueAt = new Date(now + number(relative[1] || relative[3]) * units[(relative[2] || relative[4]).toLowerCase()]).toISOString();
  } else if (day) parts = shiftDate(parts, /后天/.test(day[0]) ? 2 : /明天|tomorrow/i.test(day[0]) ? 1 : 0);
  if (clock && !(absolute && /[T ]\d{2}:/.test(absolute[0]))) {
    let hour = number(clock[1]);
    if (/下午|晚上|今晚|PM/i.test(clock[0]) && hour < 12) hour += 12;
    if (/AM/i.test(clock[0]) && hour === 12) hour = 0;
    parts = { ...parts, hour, minute: clock[2] ? Number(clock[2]) : clock[3] === '半' ? 30 : Number(clock[3] || 0) };
  }
  if (!dueAt) {
    if (interval && !clock && !absolute && !day) dueAt = new Date(now + intervalMS).toISOString();
    else {
      if (!clock && !(absolute && /[T ]\d{2}:/.test(absolute[0]))) throw new Error('请补充执行时间，例如明天早上 9 点。 Specify an execution time, such as tomorrow at 9 AM.');
      dueAt = zonedTime(wallString(parts), timezone);
      const spec = recurrenceSpec(recurrence, timezone, dueAt);
      if (monthlyDay) spec.calendar.day = monthlyDay;
      if (spec.calendar.kind === 'weekdays') {
        while ([0, 6].includes(new Date(Date.UTC(parts.year, parts.month - 1, parts.day)).getUTCDay())) { parts = shiftDate(parts, 1); dueAt = zonedTime(wallString(parts), timezone); }
      }
      if (Date.parse(dueAt) <= now && recurrence !== 'none') dueAt = nextCalendarTime(dueAt, spec.calendar);
      else if (Date.parse(dueAt) <= now && !day && !absolute) dueAt = zonedTime(wallString(shiftDate(parts, 1)), timezone);
    }
  }
  let content = request;
  const merged = [];
  for (const span of spans.sort((a, b) => a[0] - b[0])) {
    const previous = merged.at(-1);
    if (previous && span[0] <= previous[1]) previous[1] = Math.max(previous[1], span[1]); else merged.push([...span]);
  }
  for (const [start, end] of merged.reverse()) content = content.slice(0, start) + content.slice(end);
  content = content.replace(/^[\s,，。:：]+|[\s,，。:：]+$/g, '').trim();
  if (!content) throw new Error('请补充要执行的任务。 Describe what the task should do.');
  const spec = interval ? { intervalMS, calendar: {} } : recurrenceSpec(recurrence, timezone, dueAt);
  if (monthlyDay) spec.calendar.day = monthlyDay;
  return { content, dueAt, ...spec };
}

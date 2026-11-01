package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

// ---------- Persian text ----------

var faDigitsR = strings.NewReplacer("0", "۰", "1", "۱", "2", "۲", "3", "۳", "4", "۴", "5", "۵", "6", "۶", "7", "۷", "8", "۸", "9", "۹")

func fa(v any) string {
	switch x := v.(type) {
	case int:
		return faDigitsR.Replace(strconv.Itoa(x))
	case int64:
		return faDigitsR.Replace(strconv.FormatInt(x, 10))
	case float64:
		s := strconv.FormatFloat(x, 'f', -1, 64)
		if strings.Contains(s, ".") {
			s = strconv.FormatFloat(x, 'f', 1, 64)
			s = strings.TrimSuffix(s, ".0")
		}
		return strings.ReplaceAll(faDigitsR.Replace(s), ".", "٫")
	case string:
		return faDigitsR.Replace(x)
	}
	return faDigitsR.Replace(fmt.Sprint(v))
}

var toLatinR = strings.NewReplacer("۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
	"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9", "٫", ".", "٬", "")

func latin(s string) string { return toLatinR.Replace(s) }

func parseNum(s string) (float64, bool) {
	s = strings.TrimSpace(latin(s))
	s = strings.ReplaceAll(s, ",", "")
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

var normR = strings.NewReplacer("ي", "ی", "ك", "ک", "‌", "", "‌", "", "ة", "ه", "أ", "ا", "إ", "ا", "آ", "ا")

func normName(s string) string {
	s = normR.Replace(strings.TrimSpace(s))
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func containsWord(hay, needle string) bool {
	for _, w := range strings.Fields(hay) {
		if w == needle {
			return true
		}
	}
	return strings.Contains(hay, needle) && len([]rune(needle)) >= 3
}

// ---------- Jalali calendar ----------

func toJalali(gy, gm, gd int) (int, int, int) {
	gdm := []int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	var jy int
	if gy > 1600 {
		jy = 979
		gy -= 1600
	} else {
		jy = 0
		gy -= 621
	}
	gy2 := gy
	if gm > 2 {
		gy2 = gy + 1
	}
	days := 365*gy + (gy2+3)/4 - (gy2+99)/100 + (gy2+399)/400 - 80 + gd + gdm[gm-1]
	jy += 33 * (days / 12053)
	days %= 12053
	jy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		jy += (days - 1) / 365
		days = (days - 1) % 365
	}
	var jm, jd int
	if days < 186 {
		jm = 1 + days/31
		jd = 1 + days%31
	} else {
		jm = 7 + (days-186)/30
		jd = 1 + (days-186)%30
	}
	return jy, jm, jd
}

var jMonths = []string{"", "فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور", "مهر", "آبان", "آذر", "دی", "بهمن", "اسفند"}
var faWeekdays = []string{"یکشنبه", "دوشنبه", "سه‌شنبه", "چهارشنبه", "پنجشنبه", "جمعه", "شنبه"}

// "سه‌شنبه ۱۳ آبان"
func faDate(t time.Time) string {
	_, m, d := toJalali(t.Year(), int(t.Month()), t.Day())
	return faWeekdays[t.Weekday()] + " " + fa(d) + " " + jMonths[m]
}

func faDateYear(t time.Time) string {
	y, _, _ := toJalali(t.Year(), int(t.Month()), t.Day())
	return faDate(t) + " " + fa(y)
}

// "1405/08/13"
func jalaliNum(t time.Time) string {
	y, m, d := toJalali(t.Year(), int(t.Month()), t.Day())
	return fmt.Sprintf("%04d/%02d/%02d", y, m, d)
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

func parseDay(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	return t, err == nil
}

// faDay: "2026-11-04" -> "سه‌شنبه ۱۳ آبان"
func faDay(s string) string {
	t, ok := parseDay(s, time.UTC)
	if !ok {
		return s
	}
	return faDate(t)
}

// relative label from today's date
func relDay(day, today string) string {
	t, ok1 := parseDay(day, time.UTC)
	n, ok2 := parseDay(today, time.UTC)
	if !ok1 || !ok2 {
		return day
	}
	diff := int(t.Sub(n).Hours() / 24)
	switch diff {
	case 0:
		return "امروز"
	case 1:
		return "فردا"
	case 2:
		return "پس‌فردا"
	case -1:
		return "دیروز"
	}
	return faDate(t)
}

// ETA choice -> date, counted from person's local today.
// Iranian week ends on Friday.
func etaFromChoice(choice string, today time.Time) string {
	switch choice {
	case "tomorrow":
		return dayKey(today.AddDate(0, 0, 1))
	case "dayafter":
		return dayKey(today.AddDate(0, 0, 2))
	case "week":
		d := (int(time.Friday) - int(today.Weekday()) + 7) % 7
		if d < 2 {
			d += 7
		}
		return dayKey(today.AddDate(0, 0, d))
	case "nextweek":
		d := (int(time.Friday) - int(today.Weekday()) + 7) % 7
		return dayKey(today.AddDate(0, 0, d+7))
	case "today":
		return dayKey(today)
	}
	return ""
}

var progressLabels = map[string]string{
	"start": "تازه شروع",
	"half":  "حدود نصف",
	"near":  "نزدیک تمام",
	"done":  "تمام شد",
}

var cycleLabels = map[string]string{"market": "بازار", "value": "ارزش", "cash": "نقد"}

var roleLabels = map[string]string{
	"staff": "نیرو", "lead": "مسئول خط", "finance": "مالی", "senior": "همکار ارشد", "admin": "مدیر",
}

// ---------- time of day ----------

func hm(s string) (int, bool) {
	s = latin(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func loadLoc(tz string) *time.Location {
	if tz == "" {
		tz = "Asia/Tehran"
	}
	l, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return l
}

func minutesOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }

func shorten(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

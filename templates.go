package main

const templates = `
{{define "head"}}<!doctype html>
<html lang="fa" dir="rtl"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>روزنگار</title>
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Vazirmatn:wght@400;500;700;800;900&display=swap">
<style>
:root{--honey:#EFC92F;--honey-d:#DEB41C;--honey-50:#FEF9E9;--honey-100:#FDF1C9;--honey-800:#8C6E10;--honey-900:#5E4A0A;
--ink:#232323;--ink-8:#303030;--ink-6:#505050;--ink-5:#6B6B6B;--ink-3:#C0C0C0;--ink-2:#E0E0E0;--ink-1:#F2F2F2;--page:#F9F9F9;
--ok:#2E9E5B;--ok-bg:#E4F4EA;--red:#D64545;--red-ink:#B83535;--red-bg:#FBE9E9;--r:14px}
*{box-sizing:border-box}
body{margin:0;background:var(--page);color:var(--ink-8);font-family:"YekanBakh","Vazirmatn",Tahoma,sans-serif;font-size:14.5px;line-height:1.7}
a{color:var(--honey-800);text-decoration:none}a:hover{color:var(--honey-900)}
h1,h2,h3{color:var(--ink);margin:0;line-height:1.4}
h2{font-size:20px;font-weight:800}h3{font-size:16px;font-weight:700}
header.top{background:var(--ink-8);color:#fff}
.wrap{max-width:1360px;margin:0 auto;padding:0 20px}
.top .wrap{display:flex;flex-wrap:wrap;align-items:center;gap:12px 24px;padding-block:12px}
.brand{display:flex;align-items:center;gap:10px;color:#fff;font-weight:800;font-size:18px}
.hex{display:inline-block;width:12px;height:12px;background:var(--honey);clip-path:polygon(25% 5%,75% 5%,100% 50%,75% 95%,25% 95%,0% 50%);flex-shrink:0}
.brand .hex{width:34px;height:34px;display:flex;align-items:center;justify-content:center;color:var(--ink);font-weight:900}
nav.main{display:flex;flex-wrap:wrap;gap:4px;flex:1}
nav.main a{padding:7px 13px;border-radius:8px;color:#E0E0E0;font-size:14px}
nav.main a.on{background:var(--honey);color:var(--ink);font-weight:700}
.who{display:flex;align-items:center;gap:10px;color:var(--ink-3);font-size:13px}
.who button{background:none;border:1px solid var(--ink-6);color:#fff;border-radius:8px;padding:4px 10px;font:inherit;font-size:12.5px;cursor:pointer}
main.wrap{padding-top:22px;padding-bottom:60px;display:flex;flex-direction:column;gap:22px}
.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:14px}
.tile{background:#fff;border:1px solid var(--ink-2);border-radius:var(--r);padding:14px 16px}
.tile.dark{background:var(--ink-8);border-color:var(--ink-8);color:#fff}.tile.dark .l{color:var(--ink-3)}
.tile.brand{background:var(--honey);border-color:var(--honey)}
.tile .v{font-size:28px;font-weight:900;line-height:1.2}.tile .l{font-size:13px;color:var(--ink-6)}
.cols{display:flex;flex-wrap:wrap;gap:22px;align-items:flex-start}
.colmain{flex:999 1 640px;min-width:0;display:flex;flex-direction:column;gap:16px}
.colside{flex:1 1 330px;min-width:0;display:flex;flex-direction:column;gap:16px}
.card{background:#fff;border:1px solid var(--ink-2);border-radius:var(--r);padding:16px;display:flex;flex-direction:column;gap:10px}
.card.honey{background:var(--honey-50);border:1.5px solid #F6D963}
.card.dark{background:var(--ink-8);color:#fff;border-color:var(--ink-8)}.card.dark h2,.card.dark h3{color:#fff}
.scroll{overflow-x:auto;background:#fff;border:1px solid var(--ink-2);border-radius:var(--r)}
table{width:100%;border-collapse:collapse;font-size:13.5px}
th{background:var(--ink-8);color:#fff;text-align:right;font-weight:700;padding:10px 12px;white-space:nowrap}
td{padding:10px 12px;border-top:1px solid var(--ink-1);vertical-align:top}
tbody th{background:none;color:var(--ink);white-space:normal;border-top:1px solid var(--ink-1)}
.cell{display:flex;gap:8px;align-items:flex-start}.cell .hex{margin-top:6px}
.c-ok .hex,.hex.ok{background:var(--ok)}.c-wip .hex,.hex.wip{background:var(--honey-d)}.c-red .hex,.hex.red{background:var(--red)}.c-none .hex,.hex.none{background:var(--ink-3)}
.muted{color:var(--ink-5);font-size:12.5px}
.row{display:flex;gap:10px;align-items:center;flex-wrap:wrap}
.between{display:flex;justify-content:space-between;gap:10px;align-items:flex-start}
.people{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:14px}
.bar{height:8px;border-radius:4px;background:var(--ink-1);overflow:hidden}.bar i{display:block;height:100%;background:var(--ok);border-radius:4px}
.bar i.red{background:var(--red)}.bar i.wip{background:var(--honey)}
.pill{display:inline-block;font-size:12px;font-weight:700;padding:1px 10px;border-radius:999px;background:var(--ink-1);color:var(--ink-6)}
.pill.ok{background:var(--ok-bg);color:#1F6E3F}.pill.red{background:var(--red-bg);color:#9E2C2C}.pill.wip{background:var(--honey-100);color:var(--honey-900)}
ul.plain{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:6px}
ul.plain li{display:flex;gap:8px;align-items:flex-start}ul.plain li .hex{margin-top:7px;width:9px;height:9px}
.btn{display:inline-flex;align-items:center;justify-content:center;min-height:38px;padding:0 14px;border-radius:8px;border:1.5px solid var(--ink-2);background:#fff;color:var(--ink-8);font:inherit;font-size:13.5px;cursor:pointer}
.btn:hover{border-color:var(--ink-5)}
.btn.primary{background:var(--honey);border-color:var(--honey);color:var(--ink);font-weight:700}.btn.primary:hover{background:var(--honey-d)}
.btn.dark{background:var(--ink-8);border-color:var(--ink-8);color:#fff}
.btn.sm{min-height:32px;padding:0 10px;font-size:12.5px}
form.inline{display:inline}
.form{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:12px}
.form .full{grid-column:1/-1}
label{display:flex;flex-direction:column;gap:4px;font-size:13px;color:var(--ink-6);font-weight:500}
input,select,textarea{font:inherit;color:var(--ink);background:#fff;border:1.5px solid var(--ink-2);border-radius:8px;padding:7px 10px;min-height:40px;width:100%}
textarea{min-height:80px;resize:vertical}
input:focus,select:focus,textarea:focus,.btn:focus-visible,a:focus-visible{outline:2px solid var(--honey-d);outline-offset:1px}
.checks{display:flex;flex-wrap:wrap;gap:8px 14px}.checks label{flex-direction:row;align-items:center;gap:6px;color:var(--ink-8)}
.checks input{width:auto;min-height:auto}
.note{background:var(--honey-50);border:1px dashed var(--honey-d);border-radius:10px;padding:10px 12px;font-size:13px;color:var(--ink-6)}
.code{font-family:ui-monospace,monospace;direction:ltr;text-align:left;background:var(--ink-1);border-radius:6px;padding:2px 8px;font-size:13px;word-break:break-all}
.split{display:flex;height:12px;border-radius:6px;overflow:hidden;background:#505050}
.login{max-width:380px;margin:12vh auto;background:#fff;border:1px solid var(--ink-2);border-radius:var(--r);padding:24px;display:flex;flex-direction:column;gap:14px}
.err{color:var(--red-ink);font-size:13px}
@media (max-width:640px){.wrap{padding:0 14px}nav.main a{padding:6px 10px}}
</style></head><body>{{end}}

{{define "nav"}}<header class="top"><div class="wrap">
<div class="brand"><span class="hex">ر</span>روزنگار</div>
<nav class="main" aria-label="بخش‌ها">
<a href="/" {{if eq .Page "today"}}class="on"{{end}}>امروز</a>
<a href="/tasks" {{if eq .Page "tasks"}}class="on"{{end}}>کارها و زنجیره</a>
{{if ne .U.Role "finance"}}<a href="/people" {{if eq .Page "people"}}class="on"{{end}}>آدم‌ها</a>{{end}}
{{if or (eq .U.Role "admin") (eq .U.Role "senior")}}<a href="/plans" {{if eq .Page "plans"}}class="on"{{end}}>برنامهٔ فردا</a>
<a href="/projects" {{if eq .Page "projects"}}class="on"{{end}}>پروژه‌ها</a>{{end}}
{{if ne .U.Role "finance"}}<a href="/drafts" {{if eq .Page "drafts"}}class="on"{{end}}>پیام‌ها</a>
<a href="/broadcast" {{if eq .Page "broadcast"}}class="on"{{end}}>ارسال برای همه</a>{{end}}
{{if or (eq .U.Role "admin") (eq .U.Role "senior")}}<a href="/log" {{if eq .Page "log"}}class="on"{{end}}>رویدادها</a>{{end}}
{{if eq .U.Role "admin"}}<a href="/users" {{if eq .Page "users"}}class="on"{{end}}>کاربران و Claude</a>{{end}}
</nav>
<div class="who"><span>{{.Today}}</span><span>{{.U.Name}} · {{.RoleLabel}}</span>
<form method="post" action="/logout" class="inline"><button>خروج</button></form></div>
</div></header>{{end}}

{{define "foot"}}</body></html>{{end}}

{{define "login"}}{{template "head"}}
<form class="login" method="post" action="/login">
<div class="row"><span class="hex" style="width:30px;height:30px"></span><h2>روزنگار</h2></div>
{{if .Err}}<p class="err">نام کاربری یا رمز درست نیست.</p>{{end}}
<label>نام کاربری<input name="login" autocomplete="username" required autofocus></label>
<label>رمز<input name="password" type="password" autocomplete="current-password" required></label>
<button class="btn primary">ورود</button>
</form>{{template "foot"}}{{end}}

{{define "today"}}{{template "head"}}{{template "nav" .}}
<main class="wrap">
<section class="tiles" aria-label="خلاصه">
<div class="tile dark"><div class="v">{{fa .Reported}} از {{fa .Expected}}</div><div class="l">گزارش امروز</div></div>
<div class="tile brand"><div class="v">{{fa (len .Blockers)}}</div><div class="l">گره باز</div></div>
<div class="tile"><div class="v">{{fa (len .Hand)}}</div><div class="l">حلقه‌هایی که امروز به نفر بعد رسید</div></div>
<div class="tile"><div class="v">{{fa .Waiting}}</div><div class="l">منتظر شما (تأیید، برنامه، پیام)</div></div>
</section>
<div class="cols">
<div class="colmain">
<div class="between"><h2>سه زنجیرهٔ هر پروژه</h2><span class="muted">هر خانه: حلقهٔ فعلی و کسی که دستش است</span></div>
<div class="scroll"><table style="min-width:720px">
<thead><tr><th style="width:22%">پروژه</th><th>بازار</th><th>ارزش</th><th>نقد</th></tr></thead>
<tbody>{{range .Chains}}<tr><th scope="row"><a href="/tasks?project={{.Project.ID}}&status=all">{{.Project.Name}}</a><div class="muted">{{.Project.Sub}}</div></th>
{{range .Cells}}<td><div class="cell c-{{.Color}}"><span class="hex"></span><div><div>{{.Text}}</div>{{if .Who}}<div class="muted">{{.Who}}</div>{{end}}</div></div></td>{{end}}</tr>
{{else}}<tr><td colspan="4" class="muted">هنوز پروژه‌ای نیست. از «پروژه‌ها» شروع کنید.</td></tr>{{end}}</tbody></table></div>
<div class="row muted"><span class="row"><span class="hex ok"></span>بسته‌شده</span><span class="row"><span class="hex wip"></span>در جریان</span><span class="row"><span class="hex red"></span>گره</span><span class="row"><span class="hex none"></span>حلقهٔ بی‌صاحب</span></div>

<h2 style="margin-top:8px">آدم‌ها امروز</h2>
<div class="people">{{range .People}}<article class="card">
<div class="between"><h3>{{.P.Name}}</h3><span class="pill {{if .StateOK}}ok{{else}}wip{{end}}">{{.State}}</span></div>
{{if .Metric}}<div><b>{{.Metric}}</b></div>{{end}}
<ul class="plain">{{range .Items}}<li><span class="hex"></span><span>{{.}}</span></li>{{else}}<li class="muted">برنامه‌ای برای امروز نیست</li>{{end}}</ul>
<div class="bar"><i class="{{.Bar}}" style="width:{{pct .Pct}}"></i></div>
<div class="muted">{{chan .P.Channel}}{{if .P.Line}} · {{.P.Line}}{{end}}</div>
</article>{{else}}<p class="muted">هنوز کسی اضافه نشده.</p>{{end}}</div>
</div>

<aside class="colside">
{{if or .Approvals .Plans .Drafts}}<section class="card honey"><h2>منتظر شما</h2>
{{range .Approvals}}<div><b>{{.Title}}</b><div class="muted" style="white-space:pre-line">{{.Sub}}</div>
{{if eq $.U.Role "admin"}}<div class="row" style="margin-top:6px">
<form method="post" action="/approvals/{{.ID}}/ok" class="inline"><button class="btn primary sm">تأیید</button></form>
<form method="post" action="/approvals/{{.ID}}/ask" class="inline"><button class="btn sm">بپرس چرا</button></form>
<form method="post" action="/approvals/{{.ID}}/no" class="inline"><button class="btn sm">رد</button></form></div>{{end}}</div>{{end}}
{{if .Plans}}<div><b>برنامه‌های فردا ({{fa (len .Plans)}})</b>{{range .Plans}}<div class="muted">{{.Title}}: {{.Sub}}</div>{{end}}
<a class="btn primary sm" href="/plans" style="margin-top:6px">مرور و تأیید</a></div>{{end}}
{{if .Drafts}}<div><b>پیام‌های آماده ({{fa (len .Drafts)}})</b>{{range .Drafts}}<div class="muted">{{.Title}}: {{.Sub}}</div>{{end}}
<a class="btn sm" href="/drafts" style="margin-top:6px">بفرست یا اصلاح کن</a></div>{{end}}
</section>{{end}}

<section class="card"><h2>گره‌های باز</h2>
{{range .Blockers}}<div class="between"><div><div><span class="hex red" style="margin-left:6px"></span>{{.Title}}</div><div class="muted">{{.Sub}}</div></div>
<form method="post" action="/blockers/{{.ID}}/resolve" class="inline"><button class="btn sm">باز شد</button></form></div>
{{else}}<p class="muted">گرهی باز نیست.</p>{{end}}</section>

<section class="card"><h2>حلقه‌هایی که امروز رد شد</h2>
<ul class="plain">{{range .Hand}}<li><span class="hex ok"></span><span>{{.}}</span></li>{{else}}<li class="muted">امروز تحویلی به حلقهٔ بعد نرسیده.</li>{{end}}</ul></section>

<section class="card"><h2>پیگیری‌ها</h2>
{{range .Follows}}<div><div>{{.Title}}</div><div class="muted">{{.Sub}}</div></div>{{else}}<p class="muted">پیگیری بازی نیست. از «پیام‌ها» یا Claude بفرستید.</p>{{end}}</section>

{{if .SplitAny}}<section class="card dark"><h3>وقت تیم این هفته</h3>
<div class="split" role="img" aria-label="بازار {{split .Split "market"}}، ارزش {{split .Split "value"}}، نقد {{split .Split "cash"}} درصد">
<div style="width:{{pct (split .Split "market")}};background:#9CC0EA"></div><div style="width:{{pct (split .Split "value")}};background:#EFC92F"></div><div style="width:{{pct (split .Split "cash")}};background:#7FD3A6"></div></div>
<div class="row" style="color:#E0E0E0;font-size:13px"><span>بازار {{fa (split .Split "market")}}٪</span><span>ارزش {{fa (split .Split "value")}}٪</span><span>نقد {{fa (split .Split "cash")}}٪</span></div>
<div style="color:#C0C0C0;font-size:12.5px">بر اساس کارهایی که این هفته جابه‌جا شدند.</div></section>{{end}}
</aside></div></main>{{template "foot"}}{{end}}

{{define "people"}}{{template "head"}}{{template "nav" .}}
<main class="wrap">
<div class="between"><h2>آدم‌ها</h2>{{if eq .U.Role "admin"}}<a class="btn primary" href="/people?new=1">+ نفر تازه</a>{{end}}</div>
{{with .Edit}}<form class="card" method="post" action="/people/save">
<input type="hidden" name="id" value="{{.ID}}">
<h3>{{if .ID}}ویرایش {{.Name}}{{else}}نفر تازه{{end}}</h3>
<div class="form">
<label>نام کامل<input name="name" value="{{.Name}}" required></label>
<label>ربات چطور صدایش کند<input name="nick" value="{{.Nick}}" placeholder="مثلاً ابویحیی"></label>
<label>پیام‌رسان<select name="channel"><option value="telegram" {{if eq .Channel "telegram"}}selected{{end}}>تلگرام</option><option value="bale" {{if eq .Channel "bale"}}selected{{end}}>بله</option></select></label>
<label>منطقهٔ زمانی<input name="tz" value="{{.TZ}}" dir="ltr" placeholder="Asia/Tehran"></label>
<label>نقش<select name="role">{{$r := .Role}}{{range $.Roles}}<option value="{{.}}" {{if eq . $r}}selected{{end}}>{{role .}}</option>{{end}}</select></label>
<label>خط کاری<input name="line" value="{{.Line}}" list="lines" placeholder="گرافیک، ساخت، محتوا…"></label>
<label>ارزش روزانه (میلیون تومان)<input name="daily_value" value="{{latinN .DailyValue}}" inputmode="decimal" dir="ltr" placeholder="30"></label>
<label class="checks" style="align-self:end"><span><input type="checkbox" name="no_tune" value="1" {{if .NoTune}}checked{{end}}> ربات ساعت‌هایش را خودش تنظیم نکند</span></label>
<label class="checks" style="align-self:end"><span><input type="checkbox" name="active" value="1" {{if .Active}}checked{{end}}> فعال</span></label>
</div>
<h3>ریتم روزانه <span class="muted">(به وقت خودش؛ خالی یعنی خاموش)</span></h3>
<div class="form">
<label>برنامهٔ صبح<input name="plan" value="{{.Rhythm.Plan}}" dir="ltr" placeholder="07:00"></label>
<label>یادآوری شروع (اگر شروع نزده)<input name="nudge" value="{{.Rhythm.Nudge}}" dir="ltr" placeholder="09:40"></label>
<label>پیام ظهر<input name="noon" value="{{.Rhythm.Noon}}" dir="ltr" placeholder="12:30"></label>
<label>گزارش عصر<input name="report" value="{{.Rhythm.Report}}" dir="ltr" placeholder="16:30"></label>
</div>
<div class="checks"><span class="muted">روزهای تعطیل:</span>{{range $.Weekdays}}<label><input type="checkbox" name="off" value="{{latinI .N}}" {{if has $.OffSet .N}}checked{{end}}> {{.Name}}</label>{{end}}</div>
<label>یادداشت مدیریتی (فقط برای شما و دستیار؛ به خودش نشان داده نمی‌شود)<textarea name="notes">{{.Notes}}</textarea></label>
{{if .ID}}<div class="note">{{if .ChatID}}وصل است ({{chan .Channel}}).{{else}}هنوز وصل نشده. کد اتصال: <span class="code">{{.LinkCode}}</span> را برایش بفرستید تا در ربات {{chan .Channel}} بزند.{{end}}</div>{{end}}
<div class="row"><button class="btn primary">ذخیره</button><a class="btn" href="/people">انصراف</a></div>
</form>
{{if and .ID (eq $.U.Role "admin")}}<form method="post" action="/people/{{.ID}}/code" class="inline"><button class="btn sm">کد اتصال تازه (قطع اتصال فعلی)</button></form>{{end}}
{{end}}
<datalist id="lines">{{range .Lines}}<option value="{{.}}">{{end}}</datalist>
<div class="scroll"><table><thead><tr><th>نام</th><th>نقش و خط</th><th>پیام‌رسان</th><th>ریتم</th><th>وضعیت</th><th></th></tr></thead><tbody>
{{range .People}}<tr><td><b>{{.Name}}</b></td><td>{{role .Role}}{{if .Line}} · {{.Line}}{{end}}</td><td>{{chan .Channel}}</td>
<td class="muted" dir="ltr" style="text-align:right">{{.Rhythm.Plan}} {{.Rhythm.Nudge}} {{.Rhythm.Noon}} {{.Rhythm.Report}}</td>
<td>{{if not .Active}}<span class="pill">غیرفعال</span>{{else if .ChatID}}<span class="pill ok">وصل</span>{{else}}<span class="pill wip">کد: {{.LinkCode}}</span>{{end}}</td>
<td>{{if eq $.U.Role "admin"}}<a href="/people?edit={{.ID}}">ویرایش</a>{{end}} · <a href="/tasks?person={{.ID}}">کارها</a></td></tr>{{end}}
</tbody></table></div>
{{if not .BotNames}}<p class="note">هنوز توکن هیچ رباتی در تنظیمات نیست.</p>{{end}}
</main>{{template "foot"}}{{end}}

{{define "tasks"}}{{template "head"}}{{template "nav" .}}
<main class="wrap">
<div class="between"><h2>کارها و زنجیره</h2>{{if ne .U.Role "finance"}}<a class="btn primary" href="/tasks?new=1&person={{.QPerson}}&project={{.QProject}}">+ کار تازه</a>{{end}}</div>
<form class="row" method="get" action="/tasks">
<select name="person" aria-label="نفر"><option value="">همه</option>{{range .People}}<option value="{{.ID}}" {{if eq .ID $.QPerson}}selected{{end}}>{{.Name}}</option>{{end}}</select>
<select name="project" aria-label="پروژه"><option value="">همهٔ پروژه‌ها</option>{{range .Projects}}<option value="{{.ID}}" {{if eq .ID $.QProject}}selected{{end}}>{{.Name}}</option>{{end}}</select>
<select name="status" aria-label="وضعیت"><option value="open" {{if eq .Status "open"}}selected{{end}}>باز</option><option value="done" {{if eq .Status "done"}}selected{{end}}>تمام‌شده</option><option value="all" {{if eq .Status "all"}}selected{{end}}>همه</option></select>
<button class="btn">نمایش</button></form>

{{with .Edit}}<form class="card" method="post" action="/tasks/save">
<input type="hidden" name="id" value="{{.ID}}"><input type="hidden" name="back" value="/tasks?person={{.PersonID}}">
<h3>{{if .ID}}ویرایش کار{{else}}کار تازه{{end}}</h3>
<div class="form">
<label class="full">عنوان (کوتاه، با فعل یا خروجی: «تایپوی تابلو ۳۳ تا ۳۶»)<input name="title" value="{{.Title}}" required></label>
<label>نفر<select name="person" required><option value="">انتخاب</option>{{$p := .PersonID}}{{range $.People}}<option value="{{.ID}}" {{if eq .ID $p}}selected{{end}}>{{.Name}}</option>{{end}}</select></label>
<label>پروژه<select name="project"><option value="">بدون پروژه</option>{{$pr := .ProjectID}}{{range $.Projects}}<option value="{{.ID}}" {{if eq .ID $pr}}selected{{end}}>{{.Name}}</option>{{end}}</select></label>
<label>زنجیره<select name="cycle"><option value="market" {{if eq .Cycle "market"}}selected{{end}}>بازار</option><option value="value" {{if eq .Cycle "value"}}selected{{end}}>ارزش</option><option value="cash" {{if eq .Cycle "cash"}}selected{{end}}>نقد</option></select></label>
<label>نوع<select name="kind"><option value="single" {{if eq .Kind "single"}}selected{{end}}>یک‌روزه</option><option value="multi" {{if eq .Kind "multi"}}selected{{end}}>چندروزه (پیشرفت و موعد پرسیده می‌شود)</option><option value="metric" {{if eq .Kind "metric"}}selected{{end}}>عددی (هر روز یک عدد)</option></select></label>
<label>بعد از این حلقه (منتظر چه کاری است؟)<select name="after"><option value="">هیچ؛ آماده است</option>{{$af := .After}}{{$me := .ID}}{{range $.AllTasks}}{{if ne .ID $me}}<option value="{{.ID}}" {{if eq .ID $af}}selected{{end}}>{{.Title}}</option>{{end}}{{end}}</select></label>
<label>اولویت (عدد بزرگ‌تر = زودتر)<input name="priority" value="{{latinI .Priority}}" inputmode="numeric" dir="ltr"></label>
<label>موعد<input name="eta" type="date" value="{{.ETA}}"></label>
<label>ارزش کار (میلیون تومان)<input name="value" value="{{latinN .Value}}" inputmode="decimal" dir="ltr"></label>
<label>واحد (برای عددی)<input name="unit" value="{{.Unit}}" placeholder="متر"></label>
<label>هدف روزانه<input name="daily_target" value="{{latinN .DailyTarget}}" inputmode="decimal" dir="ltr"></label>
<label>کل کار<input name="total" value="{{latinN .Total}}" inputmode="decimal" dir="ltr"></label>
<label class="full">نکته یا لینک فایل‌ها (در برنامهٔ صبح برایش می‌رود)<textarea name="detail">{{.Detail}}</textarea></label>
{{if or (eq $.U.Role "admin") (eq $.U.Role "finance")}}<label class="checks"><span><input type="checkbox" name="private" value="1" {{if .Private}}checked{{end}}> مالی (فقط مدیر و مالی می‌بینند)</span></label>{{end}}
{{if not .ID}}<label class="checks"><span><input type="checkbox" name="notify" value="1" checked> ربات خودش به او خبر بدهد</span></label>{{end}}
</div>
<div class="row"><button class="btn primary">ذخیره</button><a class="btn" href="/tasks">انصراف</a></div>
</form>{{end}}

<div class="scroll"><table style="min-width:860px"><thead><tr><th>کار</th><th>نفر</th><th>زنجیره</th><th>وضعیت</th><th>منتظر</th><th></th></tr></thead><tbody>
{{range .Tasks}}<tr>
<td><b>{{.Title}}</b>{{if .Project}}<div class="muted">{{.Project}} · {{kind .Kind}}</div>{{end}}
{{range .Links}}<div><a href="{{.ID}}">{{.Title}}</a></div>{{end}}</td>
<td>{{.Person}}</td><td>{{cycle .Cycle}}</td>
<td>{{if eq .Status "done"}}<span class="pill ok">تمام</span>{{else if .Blocked}}<span class="pill red">گره</span>{{else if not .Ready}}<span class="pill">منتظر حلقهٔ قبل</span>{{else if .Progress}}<span class="pill wip">{{prog .Progress}}</span>{{else}}<span class="pill wip">آماده</span>{{end}}
{{if .ETA}}<div class="muted">موعد {{faDay .ETA}}{{if gt (len .ETAHistory) 1}} · {{fa (len .ETAHistory)}} تخمین{{end}}</div>{{end}}
{{if eq .Kind "metric"}}<div class="muted">{{fa .DoneQty}}{{if .Total}} از {{fa .Total}}{{end}} {{.Unit}}</div>{{end}}</td>
<td class="muted">{{.AfterTitle}}</td>
<td style="white-space:nowrap"><a href="/tasks?edit={{.ID}}">ویرایش</a>
{{if eq .Status "open"}}<form method="post" action="/tasks/{{.ID}}/done" class="inline"><button class="btn sm">تمام شد</button></form>
<form method="post" action="/tasks/{{.ID}}/drop" class="inline"><button class="btn sm">کنار بگذار</button></form>
{{else if eq .Status "done"}}<form method="post" action="/tasks/{{.ID}}/reopen" class="inline"><button class="btn sm">بازگشایی</button></form>{{end}}</td>
</tr>{{else}}<tr><td colspan="6" class="muted">کاری نیست.</td></tr>{{end}}
</tbody></table></div></main>{{template "foot"}}{{end}}

{{define "projects"}}{{template "head"}}{{template "nav" .}}
<main class="wrap"><div class="between"><h2>پروژه‌ها</h2><a class="btn primary" href="/projects?new=1">+ پروژهٔ تازه</a></div>
{{with .Edit}}<form class="card" method="post" action="/projects/save"><input type="hidden" name="id" value="{{.ID}}">
<div class="form"><label>نام<input name="name" value="{{.Name}}" required></label><label>زیرعنوان (کارفرما، اندازه، مسئول)<input name="sub" value="{{.Sub}}"></label>
<label>نام پروژه در بارگذار (مثلاً harfe-no؛ برای لینک آپلود)<input name="bargozar" value="{{.Bargozar}}" dir="ltr" placeholder="harfe-no"></label>
<label>ترتیب<input name="order" value="{{latinI .Order}}" dir="ltr"></label><label class="checks" style="align-self:end"><span><input type="checkbox" name="active" value="1" {{if .Active}}checked{{end}}> فعال</span></label></div>
<div class="row"><button class="btn primary">ذخیره</button><a class="btn" href="/projects">انصراف</a></div></form>{{end}}
<div class="scroll"><table><thead><tr><th>نام</th><th>زیرعنوان</th><th>بارگذار</th><th>وضعیت</th><th></th></tr></thead><tbody>
{{range .Projects}}<tr><td><b>{{.Name}}</b></td><td class="muted">{{.Sub}}</td><td class="muted" dir="ltr" style="text-align:right">{{.Bargozar}}</td><td>{{if .Active}}<span class="pill ok">فعال</span>{{else}}<span class="pill">بسته</span>{{end}}</td>
<td><a href="/projects?edit={{.ID}}">ویرایش</a> · <a href="/tasks?project={{.ID}}&status=all">کارها</a></td></tr>{{end}}</tbody></table></div></main>{{template "foot"}}{{end}}

{{define "plans"}}{{template "head"}}{{template "nav" .}}
<main class="wrap"><div class="between"><h2>برنامهٔ فردا</h2>
<form method="post" action="/plans/draft" class="inline"><button class="btn">ساختن پیش‌نویس‌ها از کارهای آماده</button></form></div>
<p class="note">{{if eq .Mode "wait"}}برنامه‌ای که تأیید نشود صبح نمی‌رود؛ ساعت برنامهٔ آن نفر به شما خبر داده می‌شود.{{else}}اگر تا صبح تأیید نکنید، همین پیش‌نویس خودکار می‌رود.{{end}}</p>
{{range .Plans}}<form class="card" method="post" action="/plans/{{.ID}}/save">
<div class="between"><h3>{{.Person}} · {{.When}}</h3><span class="pill {{if eq .Status "draft"}}wip{{else}}ok{{end}}">{{if eq .Status "draft"}}پیش‌نویس{{else if eq .Status "approved"}}تأیید شد{{else}}رفت{{end}}</span></div>
{{if eq .Status "sent"}}<ul class="plain">{{range .Tasks}}<li><span class="hex"></span>{{.Title}}</li>{{end}}</ul>
{{else}}<div class="checks" style="flex-direction:column;align-items:flex-start">{{$pl := .}}{{range .Open}}<label><input type="checkbox" name="task" value="{{.ID}}" {{if sel $pl.Selected .ID}}checked{{end}}> {{.Title}} <span class="muted">{{.Sub}}</span></label>{{end}}</div>
<label>یک خط برای او (اختیاری)<input name="note" value="{{.Note}}"></label>
<div class="row"><button class="btn">ذخیره</button>{{if eq $.U.Role "admin"}}<button class="btn primary" name="approve" value="1">ذخیره و تأیید</button>{{end}}</div>{{end}}
</form>{{else}}<p class="muted">پیش‌نویسی نیست. هر شب ساعت مشخص‌شده خودکار ساخته می‌شود.</p>{{end}}
</main>{{template "foot"}}{{end}}

{{define "broadcast"}}{{template "head"}}{{template "nav" .}}
<main class="wrap"><h2>ارسال برای همه</h2>
{{if .Sent}}<p class="note">ارسال شروع شد. وقتی تمام شود، نتیجه در تلگرام برایتان می‌آید و پایین همین صفحه هم ثبت می‌شود.</p>{{end}}
<form class="card" method="post" action="/broadcast" enctype="multipart/form-data"><h3>فایل تازه</h3>
<div class="form">
<label class="full">فایل (ویدیو، عکس یا هر فایلی؛ حداکثر ۵۰ مگابایت)<input type="file" name="file" required></label>
<label class="full">متن زیر فایل<textarea name="caption" placeholder="سلام به همه…"></textarea></label>
</div>
<div class="checks"><span class="muted">برای:</span>
<label><input type="radio" name="aud" value="all" checked> همهٔ وصل‌ها</label>
<label><input type="radio" name="aud" value="line"> یک خط</label>
<label><input type="radio" name="aud" value="pick"> چند نفر</label>
<label><input type="radio" name="aud" value="me"> فقط خودم (آزمایش)</label></div>
<div class="form"><label>خط<select name="line"><option value=""></option>{{range .Lines}}<option>{{.}}</option>{{end}}</select></label></div>
<div class="checks"><span class="muted">چند نفر:</span>{{range .People}}<label><input type="checkbox" name="p" value="{{.ID}}"> {{.Name}}{{if not .ChatID}} (وصل نیست){{end}}</label>{{end}}</div>
<label class="checks"><span><input type="checkbox" name="me" value="1" checked> یک نسخه هم برای خودم</span></label>
<div class="row"><button class="btn primary">بفرست</button></div>
</form>
{{if .List}}<section class="card"><h3>ارسال‌های اخیر</h3>{{range .List}}<div style="margin-bottom:12px"><b>{{.Title}}</b><div class="muted">{{.Sub}}</div>{{if .Caption}}<div class="muted" style="white-space:pre-line">{{.Caption}}</div>{{end}}</div>{{end}}</section>{{end}}
</main>{{template "foot"}}{{end}}

{{define "drafts"}}{{template "head"}}{{template "nav" .}}
<main class="wrap"><h2>پیام‌ها</h2>
<p class="note">هیچ پیامی بدون «بفرست» به کسی نمی‌رود. پیام‌هایی که Claude آماده می‌کند هم این‌جا می‌آیند.</p>
<form class="card" method="post" action="/drafts/new"><h3>پیام تازه</h3>
<div class="form"><label>به<select name="to" required>{{range .People}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label>
<label class="checks" style="align-self:end"><span><input type="checkbox" name="followup" value="1" checked> پیگیری با دکمهٔ «انجام شد / در جریان / گیر دارم»</span></label>
<label class="full">متن<textarea name="text" required></textarea></label></div>
<div class="row"><button class="btn">ذخیرهٔ پیش‌نویس</button>{{if .CanSend}}<button class="btn primary" name="send" value="1">همین حالا بفرست</button>{{end}}</div></form>
{{range .Drafts}}<div class="card honey"><b>{{.Title}}</b><div style="white-space:pre-line">{{.Sub}}</div>
<div class="row">{{if $.CanSend}}<form method="post" action="/drafts/{{.ID}}/send" class="inline"><button class="btn primary sm">بفرست</button></form>
<form method="post" action="/drafts/{{.ID}}/cancel" class="inline"><button class="btn sm">لغو</button></form>{{end}}</div></div>{{end}}
{{if .Sent}}<h3>اخیر</h3>{{range .Sent}}<div class="card"><b>{{.Title}}</b><div class="muted" style="white-space:pre-line">{{.Sub}}</div></div>{{end}}{{end}}
</main>{{template "foot"}}{{end}}

{{define "users"}}{{template "head"}}{{template "nav" .}}
<main class="wrap"><h2>کاربران داشبورد و اتصال به Claude</h2>
<section class="card dark"><h3>مصرف Claude این ماه</h3>
<div class="row" style="color:#E0E0E0"><span>{{fa .Usage.Calls}} درخواست</span><span>{{fa .Usage.In}} توکن ورودی</span><span>{{fa .Usage.Out}} توکن خروجی</span><span>{{fa .Usage.Saved}} پیام بدون Claude</span></div>
<div style="font-size:20px;font-weight:800;color:#EFC92F">حدود {{.Cost}} دلار از سقف {{.Budget}} دلار</div>
<div style="color:#C0C0C0;font-size:12.5px">مدل: {{.Model}} · تخمین با قیمت‌های تنظیمات؛ عدد دقیق را در کنسول API ببینید. بعد از سقف، ربات تا آخر ماه فقط با دکمه کار می‌کند.</div></section>
<p class="note">هر کاربر یک «نشانی کانکتور» دارد. آن را در Claude ← Settings ← Connectors ← Add custom connector بگذارد؛ از آن به بعد Claude او با همین سطح دسترسی به روزنگار وصل است. این نشانی مثل رمز است؛ به کسی ندهید. اگر لو رفت، «نشانی تازه» بزنید.</p>
<form class="card" method="post" action="/users/save"><h3>کاربر تازه یا تغییر</h3>
<div class="form"><label>نام کاربری<input name="login" required dir="ltr"></label><label>نام<input name="name"></label>
<label>نقش<select name="role">{{range .Roles}}<option value="{{.}}">{{role .}}</option>{{end}}</select></label>
<label>خط (برای مسئول خط)<select name="line"><option value=""></option>{{range .Lines}}<option>{{.}}</option>{{end}}</select></label>
<label>نفر متناظر در ربات<select name="person"><option value="">—</option>{{range .People}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label>
<label>رمز (برای کاربر موجود خالی بگذارید)<input name="password" type="password" autocomplete="new-password"></label></div>
<div class="row"><button class="btn primary">ذخیره</button></div></form>
<div class="scroll"><table style="min-width:760px"><thead><tr><th>کاربر</th><th>نقش</th><th>نفر در ربات</th><th>نشانی کانکتور Claude</th><th></th></tr></thead><tbody>
{{range .Users}}<tr><td><b>{{.Name}}</b><div class="muted" dir="ltr" style="text-align:right">{{.Login}}</div></td><td>{{role .Role}}{{if .Line}} · {{.Line}}{{end}}</td><td>{{.Person}}</td>
<td><span class="code">{{.MCPURL}}</span></td>
<td><form method="post" action="/users/{{.ID}}/token" class="inline"><button class="btn sm">نشانی تازه</button></form></td></tr>{{end}}
</tbody></table></div></main>{{template "foot"}}{{end}}

{{define "log"}}{{template "head"}}{{template "nav" .}}
<main class="wrap"><h2>رویدادها</h2>
<div class="scroll"><table style="min-width:700px"><thead><tr><th>زمان</th><th>رویداد</th><th>نفر</th><th>شرح</th></tr></thead><tbody>
{{range .Events}}<tr><td class="muted" style="white-space:nowrap">{{.When}}</td><td>{{.Kind}}</td><td>{{.Who}}</td><td>{{.Text}}</td></tr>{{end}}
</tbody></table></div></main>{{template "foot"}}{{end}}
`

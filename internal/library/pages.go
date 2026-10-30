package library

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
)

// FormatSeconds renders a duration as m:ss or h:mm:ss.
func FormatSeconds(s float64) string {
	d := time.Duration(s * float64(time.Second)).Round(time.Second)
	h, m, sec := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// pathURL escapes each part of a relative path for an href.
func pathURL(parts ...string) string {
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

var indexTmpl = template.Must(template.New("index").Funcs(template.FuncMap{
	"dur":  FormatSeconds,
	"href": pathURL,
	"date": func(t time.Time) string { return t.Local().Format("Mon 2 Jan 2006, 15:04") },
	"join": func(s []string) string { return strings.Join(s, ", ") },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>tts-axi library</title>
<link rel="alternate" type="application/rss+xml" title="tts-axi" href="feed.xml">
<style>
  :root { color-scheme: light dark; --fg: #1d1d1f; --muted: #6e6e73; --bg: #fbfbfd; --card: #fff; --line: #e5e5ea; --accent: #0a66d8; }
  @media (prefers-color-scheme: dark) { :root { --fg: #f5f5f7; --muted: #a1a1a6; --bg: #111113; --card: #1c1c1f; --line: #2c2c30; --accent: #4c9bff; } }
  * { box-sizing: border-box; }
  body { margin: 0; font: 16px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: var(--bg); color: var(--fg); }
  main { max-width: 720px; margin: 0 auto; padding: 24px 16px 48px; }
  h1 { font-size: 22px; margin: 0 0 4px; }
  .sub { color: var(--muted); margin: 0 0 20px; font-size: 14px; }
  article { background: var(--card); border: 1px solid var(--line); border-radius: 12px; padding: 14px 16px; margin-bottom: 12px; }
  h2 { font-size: 17px; margin: 0 0 2px; }
  .meta { color: var(--muted); font-size: 13px; margin-bottom: 10px; }
  audio { width: 100%; }
  .links { font-size: 14px; margin-top: 8px; display: flex; gap: 14px; flex-wrap: wrap; }
  a { color: var(--accent); text-decoration: none; }
  details { font-size: 14px; margin-top: 6px; color: var(--muted); }
  .empty { color: var(--muted); }
</style>
</head>
<body>
<main>
<h1>tts-axi library</h1>
<p class="sub">{{len .}} episode{{if ne (len .) 1}}s{{end}} · <a href="feed.xml">podcast feed</a></p>
{{range .}}<article>
  <h2>{{.Title}}</h2>
  <div class="meta">{{date .Created}} · {{dur .Seconds}} · {{.Kind}} · {{join .Voices}} · {{join .Models}} · {{join .Engines}}</div>
  {{if .Player}}<div class="links"><a href="{{href .ID .Player}}">▶ Open the narrated deck</a></div>
  {{end}}<audio controls preload="none" src="{{href .ID .Audio}}"></audio>
  <div class="links"><a href="{{href .ID "transcript.md"}}">Transcript</a><a href="{{href .ID .Audio}}" download>Download</a></div>
  {{if .Chapters}}<details><summary>{{len .Chapters}} chapters</summary><ol>{{range .Chapters}}<li>{{.}}</li>{{end}}</ol></details>{{end}}
</article>
{{else}}<p class="empty">No episodes yet. Make one with <code>tts-axi say "Hello"</code>.</p>
{{end}}</main>
</body>
</html>
`))

func renderIndex(eps []Episode) string {
	var b bytes.Buffer
	if err := indexTmpl.Execute(&b, eps); err != nil {
		panic(err) // the template and its data types are fixed at compile time
	}
	return b.String()
}

// feed types: RSS 2.0 with the iTunes duration tag podcast apps read.
type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Itunes  string   `xml:"xmlns:itunes,attr"`
	Channel channel  `xml:"channel"`
}

type channel struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Language    string `xml:"language"`
	Items       []item `xml:"item"`
}

type item struct {
	Title       string    `xml:"title"`
	GUID        guid      `xml:"guid"`
	PubDate     string    `xml:"pubDate"`
	Enclosure   enclosure `xml:"enclosure"`
	Duration    string    `xml:"itunes:duration"`
	Description string    `xml:"description,omitempty"`
}

type guid struct {
	Perma bool   `xml:"isPermaLink,attr"`
	Value string `xml:",chardata"`
}

type enclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length int64  `xml:"length,attr"`
}

func renderFeed(eps []Episode, base string) string {
	base = strings.TrimRight(base, "/")
	link := func(parts ...string) string {
		p := pathURL(parts...)
		if base == "" {
			return p
		}
		return base + "/" + p
	}
	ch := channel{
		Title:       "tts-axi",
		Link:        link("index.html"),
		Description: "Audio made with tts-axi on this Mac.",
		Language:    "en",
	}
	for _, ep := range eps {
		desc := ep.Kind
		if len(ep.Chapters) > 0 {
			desc += ": " + strings.Join(ep.Chapters, "; ")
		}
		ch.Items = append(ch.Items, item{
			Title:       ep.Title,
			GUID:        guid{Value: "tts-axi:" + ep.ID},
			PubDate:     ep.Created.UTC().Format(time.RFC1123Z),
			Enclosure:   enclosure{URL: link(ep.ID, ep.Audio), Type: mimeOf(ep.Audio), Length: ep.Bytes},
			Duration:    FormatSeconds(ep.Seconds),
			Description: desc,
		})
	}
	out, err := xml.MarshalIndent(rss{Version: "2.0", Itunes: "http://www.itunes.com/dtds/podcast-1.0.dtd", Channel: ch}, "", "  ")
	if err != nil {
		panic(err)
	}
	return xml.Header + string(out) + "\n"
}

func mimeOf(name string) string {
	switch {
	case strings.HasSuffix(name, ".m4a"):
		return "audio/mp4"
	case strings.HasSuffix(name, ".wav"):
		return "audio/wav"
	}
	return "audio/mpeg"
}

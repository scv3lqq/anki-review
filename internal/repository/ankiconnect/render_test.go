package ankiconnect

import "testing"

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "What is MVCC?", "What is MVCC?"},
		{"breaks and blocks", "line one<br>line two<div>three</div><p>four</p>", "line one\nline two\nthree\nfour"},
		{"entities", "a &lt; b &amp;&amp; c&nbsp;&gt; d &#39;q&#39; &#x2192;", "a < b && c > d 'q' →"},
		{"style and script dropped", "<style>.x{}</style>Q<script>alert(1)</script>", "Q"},
		{"source backlink dropped", `key<div class="source"><a href="x">Note</a></div>`, "key"},
		{"sound and image", "[sound:a.mp3]word <img src=a.png>", "word [image]"},
		{"blank lines collapse", "<div> </div><br><br>x<br> ", "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := htmlToText(tt.in); got != tt.want {
				t.Errorf("htmlToText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		name               string
		card               cardInfo
		wantQuestion, back string
	}{
		{
			name: "basic card keeps only the answer side",
			card: cardInfo{
				ModelName: "Basic",
				Question:  "What does <code>defer</code> do?",
				Answer:    "What does <code>defer</code> do?<hr id=answer>Runs a call when the function returns",
			},
			wantQuestion: "What does `defer` do?",
			back:         "Runs a call when the function returns",
		},
		{
			name: "cloze lists the gaps, then Back Extra",
			card: cardInfo{
				ModelName: "Cloze",
				Question:  `WAL is written <span class="cloze" data-cloze="before">[...]</span> data pages`,
				Answer:    "WAL is written before data pages<br>Back extra line",
			},
			wantQuestion: "WAL is written [...] data pages",
			back:         "Gap: before\nBack extra line",
		},
		{
			name:         "answer without separator is used whole",
			card:         cardInfo{ModelName: "Basic", Question: "Q", Answer: "A"},
			wantQuestion: "Q",
			back:         "A",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, back := render(tt.card)
			if q != tt.wantQuestion || back != tt.back {
				t.Errorf("render() = %q, %q; want %q, %q", q, back, tt.wantQuestion, tt.back)
			}
		})
	}
}

func TestButtonLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"\u206810m\u2069", "10m"},
		{"\u20681d\u2069", "1d"},
		{"2.5mo", "2.5mo"},
		{"<1m", "<1m"},
		{"soon", "soon"},
	}
	for _, tt := range tests {
		if got := buttonLabel(tt.in); got != tt.want {
			t.Errorf("buttonLabel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEscapeDeck(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Tech::Go", "Tech::Go"},
		{`My "deck"`, `My \"deck\"`},
		{"a_b*c", `a\_b\*c`},
		{`back\slash`, `back\\slash`},
	}
	for _, tt := range tests {
		if got := escapeDeck(tt.in); got != tt.want {
			t.Errorf("escapeDeck(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

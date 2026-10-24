package ankiconnect

import (
	"html"
	"regexp"
	"strings"
)

var (
	reStyle   = regexp.MustCompile(`(?is)<style.*?</style>`)
	reScript  = regexp.MustCompile(`(?is)<script.*?</script>`)
	reSource  = regexp.MustCompile(`(?is)<div class="source">.*?</div>`)
	reSound   = regexp.MustCompile(`\[sound:[^\]]*\]`)
	reImg     = regexp.MustCompile(`(?i)<img[^>]*>`)
	reBreak   = regexp.MustCompile(`(?i)<br\s*/?>|</?(div|p|li)\b[^>]*>|<hr[^>]*>`)
	reTag     = regexp.MustCompile(`<[^>]+>`)
	reCode    = regexp.MustCompile(`(?is)<code[^>]*>(.*?)</code>`)
	reCloze   = regexp.MustCompile(`data-cloze="([^"]*)"`)
	reAnswer  = regexp.MustCompile(`(?i)<hr id=answer>`)
	reBidi    = regexp.MustCompile(`\p{Cf}`)
	nbspSpace = strings.NewReplacer("\u00a0", " ")
)

// htmlToText turns a card side into plain lines, the way the reviewer shows it.
func htmlToText(s string) string {
	s = reStyle.ReplaceAllString(s, "")
	// a Source block on the answer side links back to the note, it is not part of the answer
	s = reSource.ReplaceAllString(s, "")
	s = reScript.ReplaceAllString(s, "")
	s = reSound.ReplaceAllString(s, "")
	s = reImg.ReplaceAllString(s, "[image]")
	s = reBreak.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = nbspSpace.Replace(html.UnescapeString(s))

	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// withCode keeps inline code as backticks: the page renders it, the grader reads it.
func withCode(s string) string {
	return reCode.ReplaceAllString(s, "`$1`")
}

// render returns the question and the answer key of a card.
func render(c cardInfo) (question, back string) {
	question = htmlToText(withCode(c.Question))
	if strings.Contains(c.ModelName, "Cloze") {
		var answers []string
		for _, m := range reCloze.FindAllStringSubmatch(c.Question, -1) {
			answers = append(answers, htmlToText(m[1]))
		}
		lines := strings.Split(htmlToText(withCode(c.Answer)), "\n")
		// the first line is the filled-in sentence, the rest is Back Extra
		return question, strings.Join(append([]string{"Gap: " + strings.Join(answers, ", ")}, lines[1:]...), "\n")
	}
	after := c.Answer
	if parts := reAnswer.Split(c.Answer, 2); len(parts) == 2 {
		after = parts[1]
	}
	return question, htmlToText(withCode(after))
}

// buttonLabel cleans the interval Anki prints on an answer button: AnkiConnect wraps it in bidi
// isolates, which would garble the label on the page.
func buttonLabel(label string) string {
	return reBidi.ReplaceAllString(label, "")
}

// escapeDeck quotes a deck name for Anki search, where `*`, `_` and `"` are syntax.
func escapeDeck(name string) string {
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(`\"*_`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

package main

import (
	"bytes"
	"strings"
	"testing"
)

const emojiTestFixture = `# emoji-test.txt
# Version: 18.0

# group: Smileys & Emotion
1F602                                                  ; fully-qualified     # 😂 E0.6 face with tears of joy
263A FE0F                                              ; fully-qualified     # ☺️ E0.6 smiling face
263A                                                   ; unqualified         # ☺ E0.6 smiling face
2764 FE0F                                              ; fully-qualified     # ❤️ E0.6 red heart

# group: People & Body
1F44D                                                  ; fully-qualified     # 👍 E0.6 thumbs up
1F44D 1F3FB                                            ; fully-qualified     # 👍🏻 E1.0 thumbs up: light skin tone

# group: Component
1F3FB                                                  ; fully-qualified     # 🏻 E1.0 light skin tone
`

const annotationsFixture = `{"annotations":{"identity":{},"annotations":{
  "😂":{"default":["joy","laugh","lol"],"tts":["face with tears of joy"]},
  "❤":{"default":["heart","love"],"tts":["red heart"]}
}}}`

func TestGenerate(t *testing.T) {
	entries, version, err := parseEmojiTest(strings.NewReader(emojiTestFixture))
	if err != nil || version != "18.0" {
		t.Fatalf("parse: %v, version %q", err, version)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.name)
	}
	if got := strings.Join(names, ","); got != "face with tears of joy,smiling face,red heart,thumbs up" {
		t.Errorf("entries = %s (want fully-qualified only, no skin tones or components)", got)
	}
	kws, err := parseAnnotations([]byte(annotationsFixture))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := write(&out, entries, kws, version); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"😂\tface with tears of joy\tjoy|laugh|lol\n",
		"❤️\tred heart\theart|love\n", // CLDR's key lacks U+FE0F; still matched
		"👍\tthumbs up\t\n",
		"# Emoji 18.0 from",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

# Records docs/assets/demo.gif from `go-whassup demo`: fictional chats only,
# nothing real. Run `just gif` (needs vhs: https://github.com/charmbracelet/vhs).
# Inline images (previews) don't show in VHS's terminal; the rest is real.

Output docs/assets/demo.gif

Require go-whassup

Set Shell "bash"
Set FontSize 16
Set Width 1280
Set Height 760
Set Padding 24
Set Theme "Catppuccin Frappe"
Set TypingSpeed 70ms

Hide
Type "go-whassup demo" Enter
Wait+Screen /Chats/
Show
Sleep 2s

# Browse: a photo, a reaction and an edit, then a group with a mention.
Down
Sleep 1.5s
Down
Sleep 2s

# Write in the book club.
Enter
Sleep 500ms
Type "Almost done with chapter 4, no spoilers!"
Sleep 400ms
Enter
Sleep 1.5s
Escape
Sleep 800ms

# Find anyone by name.
Type "/"
Sleep 400ms
Type "kath"
Sleep 1.2s
Escape
Sleep 600ms

# Search everything stored.
Type "s"
Sleep 400ms
Type "moth"
Sleep 1.5s
Enter
Sleep 2s

# Every key, by section. (Grace's message arrived on its own while browsing.)
Type "?"
Sleep 3s
Type "x"
Sleep 4s

Type "q"
Sleep 500ms

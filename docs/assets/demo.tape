# Records the README's demo GIF and screenshots from `go-whassup demo`:
# fictional chats only, nothing real. Run `just gif` (needs vhs:
# brew install vhs). Inline images (previews) don't show in VHS's terminal.

Output docs/assets/demo.gif

Require go-whassup

Set Shell "bash"
Set FontSize 16
Set Width 1280
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 70ms

Hide
Type "go-whassup demo" Enter
Wait+Screen /Chats/
Show
Sleep 1.5s

# Ada: a photo, a reaction and an edit.
Down
Sleep 1.5s
Screenshot docs/assets/screenshot-conversation.png

# The book club: a mention, reactions and a deleted message.
Down
Sleep 1.5s
Screenshot docs/assets/screenshot-chats.png

# Write in it.
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
Type "gr"
Sleep 1.2s
Escape
Sleep 600ms

# Search everything stored.
Type "s"
Sleep 400ms
Type "moth"
Sleep 1.5s
Screenshot docs/assets/screenshot-search.png
Enter
Sleep 2s

# Every key, by section.
Type "?"
Sleep 2s
Screenshot docs/assets/screenshot-help.png
Sleep 1s
Type "x"
Sleep 2s

Type "q"
Sleep 500ms

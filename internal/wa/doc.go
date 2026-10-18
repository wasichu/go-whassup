// Package wa adapts go.mau.fi/whatsmeow for the rest of the app.
//
// It is the ONLY package allowed to import whatsmeow. It exposes a small
// interface plus typed events so the UI can be driven by a fake in tests.
// whatsmeow event handlers run on background goroutines; this package
// forwards them as plain values and never touches UI state.
//
// Pairing is phone-number code only (Client.PairPhone). Keep pairing behind
// its own method on the interface so QR pairing can be added later without
// touching the UI's event handling.
package wa

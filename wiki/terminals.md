# Terminals

Every agent omo runs is a real agent program in a terminal, and the board shows
that terminal: in the CEO tab, in a ticket's Agents tab, and in a window of its
own. You can also open your own shells. An agent's terminal is read-only until
you switch it; you write to an agent in the message box below its terminal, or
type into the terminal itself once it is writable.

- [Your own shells](#your-own-shells)
- [Writing to an agent](#writing-to-an-agent)
- [Typing into an agent](#typing-into-an-agent)
- [Copy and paste](#copy-and-paste)
- [Images](#images)
- [A terminal in its own window](#a-terminal-in-its-own-window)

## Your own shells

The **Terminals** tab of the sidebar holds your shells. **New terminal** opens
a search over your projects (type part of a name or folder); **Home folder** is
always first. A ticket's **Open terminal** action opens a shell in its worktree,
or in the working folder of a task without a project (also after it is done).

- At most 16 of your shells run at once.
- A shell in a ticket's worktree keeps that worktree: the ticket can still be
  merged and finish, and omo removes the folder when the last shell in it ends.
- Your shells do not act as an agent: `omo ai` in them acts as you.

Only you can open shells and read terminals and agent logs; agents cannot.

## Writing to an agent

Below every agent's terminal (the CEO's too, in the sidebar, in a ticket's Agents
tab and in a terminal's own window) is a message box. Nothing omo or the agent
does changes, sends or clears what you write there, takes the focus out of it or
moves it, so you can write a longer message in peace while omo types its own
lines into the terminal above.

- **Enter** sends the message, **Shift+Enter** starts a new line; the **Send**
  button does the same. The box grows with the text and then scrolls; Send stays
  the same height as the text field. A message has at most 12,000 characters.
- The whole text goes into the terminal as one message, as if you had pasted it
  and pressed Enter, so several lines arrive as one message, not as several
  submitted lines. omo writes the message and its Enter in one go: a line omo
  types at the same moment comes before or after it, never inside it. After
  sending, the box is empty; if sending fails (the terminal is disconnected, for
  example) your text stays and the reason is shown.
- Paste an image, or drop image files on the box: omo stores them as it does for
  a terminal ([Images](#images)). Each image appears at the cursor as a marker
  such as `[image 1]`, without showing its file path. Backspace at a marker
  removes the whole marker and its image; the image list also offers a remove
  button. Typing or pasting with the cursor inside a marker inserts after it
  and keeps its image. Send pastes the text and images in their draft order, each image using
  the terminal's normal image paste, then presses Enter once. The terminal's
  input stays locked for the whole message, including against omo's own lines
  and input from other views. Pasted text is cleaned of control characters, as
  in a terminal. Other files are refused with a note.
- Each terminal keeps its own draft in this browser: it survives a reload,
  switching to another ticket and back, and opening the terminal in its own
  window or bringing it back. Drafts of terminals untouched for two weeks are
  dropped.
- A single left click in a Read only agent terminal focuses the message box.
  In Writable mode, clicking focuses the terminal for direct keys. Dragging
  still selects terminal text, and scrolling keeps working in either mode.
- The box is not shown once the agent has ended.
- The box sends whatever the read-only switch says: that switch blocks only
  direct typing into the terminal.

## Read-only agent terminals

An agent's terminal starts **read-only**, so a stray key or paste cannot reach
the agent. The **Read only** switch above the terminal, next to the pop-out
button, is on while read-only and off while **Writable**. Switch to **Writable**
for what the message box cannot do: answering an interactive prompt with single
keys, arrow keys or Escape.

- Read-only blocks only what would type into the terminal: the keyboard, paste
  and dropped files. The mouse works as ever: wheel, clicks and drags are still
  passed to a program that asks for mouse events (so scrolling works in
  full-screen programs), and selecting and copying text, clicking links and
  resizing work too.
- The choice is kept per terminal for the browser session; a terminal you have
  not switched starts read-only.
- Your own shells are not agent terminals: they stay writable and have no
  message box.

## Typing into an agent

These keys work in a writable agent terminal and in your own shells.

- **Enter** submits, as in the program itself.
- **Shift+Enter** and **Ctrl+Enter** insert a line break without submitting.
  The browser cannot send these keys to a terminal program as such, so omo
  sends them as Alt+Enter, which Claude Code, Codex, Gemini and Antigravity
  all read as a line break.
- **F6** moves the focus out of the terminal, **Shift+F6** to the element
  before it.
- omo's own messages to an agent (a mention, an answer, a review result) are
  typed in as one line starting with `omo: `. omo holds such a line back while
  you type in that terminal, so it never ends up inside your text. A message
  sent from the message box is written whole, so a line omo types never ends
  up inside it either.

An ended agent's terminal shows its saved log instead.

A terminal whose connection drops reconnects on its own, and once the board's
connection is live again every disconnected terminal connects again; the
**Reconnect** button does it at once. Every connection replays the terminal's
history, and the screen is cleared first, so it shows the history once. Input
typed while disconnected is not sent ("Terminal disconnected. This input was
not sent; press Reconnect.").

## Copy and paste

Copying works the same in every terminal view: the CEO, an agent, your shells,
a ticket's Agents tab and a terminal's own window.

- **Programs that use the mouse** (Claude Code, Codex, tmux) select in their
  own screen and hand the text to the terminal; omo puts it into the clipboard
  and shows **Copied** in the terminal's corner. omo accepts this only within
  5 seconds of a key or click of yours in that terminal, and never answers a
  program that asks to read the clipboard.
- **The terminal's own selection** (a plain drag in a shell; Shift and drag
  while a program holds the mouse) is copied with **Ctrl+C** (the selection is
  then cleared, so the next Ctrl+C interrupts the program as usual; without a
  selection Ctrl+C is the interrupt), **Ctrl+Insert** or **Ctrl+Shift+C**.
  Firefox may open its inspector on Ctrl+Shift+C; use Ctrl+Insert there.
- **Paste** with **Ctrl+V**, **Ctrl+Shift+V** or **Shift+Insert**. Pasted text
  is cleaned of control characters first.
- **On a Mac**, Cmd+C and Cmd+V copy and paste, and Ctrl keeps its meaning for
  the program.

Over plain HTTP on a host name other than `localhost` (see
[Reaching omo from another machine](running.md#reaching-omo-from-another-machine))
the browser lets a page copy only during a click or key press. When it refuses,
a **Copy selection** button appears in the terminal's corner, and your next key
or click in the terminal copies the text too.

A link in a terminal whose text is not its address asks before it opens
("Open this address?").

## Images

Drop image files onto a terminal, or paste an image you copied in another
program (Ctrl+V with no text in the clipboard): omo stores each image in
`OMO_HOME/images/terminal/` and pastes its full path into the program, quoted
when it has spaces, several separated by spaces, without a line break. Claude
Code and Codex then attach the image.

- PNG, JPEG, GIF and WebP up to 10 MiB each are taken; other files are refused
  with a note in the terminal. All terminal images together may take 2 GiB.
- A read-only or ended terminal takes nothing.
- These images belong to no ticket. They are deleted the number of days after
  their upload that **Settings → Limits** sets for images ("Keep images after
  Done", 60 by default; 0 keeps them).

Images in comments and descriptions are described in
[The board](board.md#images).

## A terminal in its own window

The button in a terminal's top bar opens it alone in a browser window (not
below 768 px wide, and not for an ended terminal). The window remembers its
size per kind of terminal. Its address is `/terminal/agent/<id>` or
`/terminal/user/<id>`, never with the login token.

While the window is open, the board shows "This terminal is open in its own
window." with **Bring it back** and **Show the window**, and holds no connection
to it. When a browser blocks the window, the terminal says so and offers a
link to open it in a new tab.

Within one browser window only one view of a terminal is live, the one opened
last; another view of it says "This terminal is shown in …" with
**Show it here**.

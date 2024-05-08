package choose_fonts

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"kitty/tools/tui"
	"kitty/tools/tui/loop"
	"kitty/tools/tui/readline"
	"kitty/tools/utils"
	"kitty/tools/utils/style"
	"kitty/tools/wcswidth"
)

var _ = fmt.Print

type State int

const (
	SCANNING_FAMILIES State = iota
	LISTING_FAMILIES
	CHOOSING_FACES
)

type handler struct {
	lp                   *loop.Loop
	fonts                map[string][]ListedFont
	state                State
	err_mutex            sync.Mutex
	err_in_worker_thread error
	mouse_state          tui.MouseState
	render_count         uint
	render_lines         tui.RenderLines
	text_style           struct {
		font_sz, dpi_x, dpi_y  float64
		foreground, background string
	}

	// Listing
	rl                          *readline.Readline
	family_list                 FamilyList
	variable_data_requested_for *utils.Set[string]
}

func (h *handler) set_worker_error(err error) {
	h.err_mutex.Lock()
	defer h.err_mutex.Unlock()
	h.err_in_worker_thread = err
}

func (h *handler) get_worker_error() error {
	h.err_mutex.Lock()
	defer h.err_mutex.Unlock()
	return h.err_in_worker_thread
}

// Listing families {{{
func (h *handler) draw_search_bar() {
	h.lp.SetCursorVisible(true)
	h.lp.SetCursorShape(loop.BAR_CURSOR, true)
	sz, err := h.lp.ScreenSize()
	if err != nil {
		return
	}
	h.lp.MoveCursorTo(1, int(sz.HeightCells))
	h.lp.ClearToEndOfLine()
	h.rl.RedrawNonAtomic()
}

const SEPARATOR = "║"

func center_string(x string, width int) string {
	l := wcswidth.Stringwidth(x)
	spaces := int(float64(width-l) / 2)
	return strings.Repeat(" ", utils.Max(0, spaces)) + x
}

func (h *handler) draw_family_summary(start_x int, sz loop.ScreenSize) (err error) {
	family := h.family_list.CurrentFamily()
	if family == "" || int(sz.WidthCells) < start_x+2 {
		return nil
	}
	lines := []string{
		h.lp.SprintStyled("fg=green bold", center_string(family, int(sz.WidthCells)-start_x)),
		"",
	}
	width := int(sz.WidthCells) - start_x - 1
	add_line := func(x string) {
		lines = append(lines, style.WrapTextAsLines(x, width, style.WrapOptions{})...)
	}
	fonts := h.fonts[family]
	if len(fonts) == 0 {
		return fmt.Errorf("The family: %s has no fonts", family)
	}
	if has_variable_data_for_font(fonts[0]) {
		s := styles_in_family(family, fonts)
		for _, sg := range s.style_groups {
			styles := sg.name + ": " + strings.Join(sg.styles, ", ")
			add_line(styles)
			add_line("")
		}
		if s.has_variable_faces {
			add_line(fmt.Sprintf("This font is %s allowing for finer style control", h.lp.SprintStyled("fg=magenta", "variable")))
		}
		add_line(fmt.Sprintf("Press the %s key to choose this family", h.lp.SprintStyled("fg=yellow", "Enter")))
	} else {
		lines = append(lines, "Reading font data, please wait…")
		key := fonts[0].cache_key()
		if !h.variable_data_requested_for.Has(key) {
			h.variable_data_requested_for.Add(key)
			go func() {
				h.set_worker_error(ensure_variable_data_for_fonts(fonts...))
				h.lp.WakeupMainThread()
			}()
		}
	}

	for i, line := range lines {
		if i >= int(sz.HeightCells)-1 {
			break
		}
		h.lp.MoveCursorTo(start_x+1, i+1)
		h.lp.QueueWriteString(line)
	}
	return
}

func (h *handler) draw_listing_screen() (err error) {
	sz, err := h.lp.ScreenSize()
	if err != nil {
		return err
	}
	num_rows := max(0, int(sz.HeightCells)-1)
	mw := h.family_list.max_width + 1
	green_fg, _, _ := strings.Cut(h.lp.SprintStyled("fg=green", "|"), "|")
	lines := make([]string, 0, num_rows)
	for _, l := range h.family_list.Lines(num_rows) {
		line := l.text
		if l.is_current {
			line = strings.ReplaceAll(line, MARK_AFTER, green_fg)
			line = h.lp.SprintStyled("fg=green", ">") + h.lp.SprintStyled("fg=green bold", line)
		} else {
			line = " " + line
		}
		lines = append(lines, line)
	}
	_, _, str := h.render_lines.InRectangle(lines, 0, 0, 0, num_rows, &h.mouse_state, h.handle_click)
	h.lp.QueueWriteString(str)
	seps := strings.Repeat(SEPARATOR, num_rows)
	seps = strings.TrimSpace(seps)
	_, _, str = h.render_lines.InRectangle(strings.Split(seps, ""), mw+1, 0, 0, num_rows, &h.mouse_state)
	h.lp.QueueWriteString(str)

	if h.family_list.Len() > 0 {
		if err = h.draw_family_summary(mw+3, sz); err != nil {
			return err
		}
	}
	h.draw_search_bar()
	return
}

func (h *handler) handle_click(id string) error {
	which, data, found := strings.Cut(id, ":")
	if !found {
		return fmt.Errorf("Not a valid click id: %s", id)
	}
	switch which {
	case "family-chosen":
		if h.state == LISTING_FAMILIES {
			if h.family_list.Select(data) {
				h.draw_screen()
			} else {
				h.lp.Beep()
			}

		}
	}
	return nil
}

func (h *handler) update_family_search() {
	text := h.rl.AllText()
	if h.family_list.UpdateSearch(text) {
		h.draw_screen()
	} else {
		h.draw_search_bar()
	}
}

func (h *handler) next(delta int, allow_wrapping bool) {
	if h.family_list.Next(delta, allow_wrapping) {
		h.draw_screen()
	} else {
		h.lp.Beep()
	}
}

func (h *handler) handle_listing_key_event(event *loop.KeyEvent) (err error) {
	if event.MatchesPressOrRepeat("esc") {
		event.Handled = true
		if h.rl.AllText() != "" {
			h.rl.ResetText()
			h.update_family_search()
			h.draw_screen()
		} else {
			return fmt.Errorf("canceled by user")
		}
		return
	}
	ev := event
	if ev.MatchesPressOrRepeat("down") {
		h.next(1, true)
		ev.Handled = true
		return nil
	}
	if ev.MatchesPressOrRepeat("up") {
		h.next(-1, true)
		ev.Handled = true
		return nil
	}
	if ev.MatchesPressOrRepeat("page_down") {
		ev.Handled = true
		sz, err := h.lp.ScreenSize()
		if err == nil {
			h.next(int(sz.HeightCells)-3, false)
		}
		return nil
	}
	if ev.MatchesPressOrRepeat("page_up") {
		ev.Handled = true
		sz, err := h.lp.ScreenSize()
		if err == nil {
			h.next(3-int(sz.HeightCells), false)
		}
		return nil
	}

	if err = h.rl.OnKeyEvent(event); err != nil {
		if err == readline.ErrAcceptInput {
			return nil
		}
		return err
	}
	if event.Handled {
		h.update_family_search()
	}
	h.draw_search_bar()
	return
}

func (h *handler) handle_listing_text(text string, from_key_event bool, in_bracketed_paste bool) (err error) {
	if err = h.rl.OnText(text, from_key_event, in_bracketed_paste); err != nil {
		return err
	}
	h.update_family_search()
	return
}

// }}}

// Events {{{
func (h *handler) initialize() {
	h.lp.SetCursorVisible(false)
	h.lp.OnQueryResponse = h.on_query_response
	h.lp.QueryTerminal("font_size", "dpi_x", "dpi_y", "foreground", "background")
	h.rl = readline.New(h.lp, readline.RlInit{DontMarkPrompts: true, Prompt: "Family: "})
	h.variable_data_requested_for = utils.NewSet[string](256)
	h.draw_screen()
	initialize_variable_data_cache()
	go func() {
		h.set_worker_error(kitty_font_backend.query("list_monospaced_fonts", nil, &h.fonts))
		h.lp.WakeupMainThread()
	}()
}

func (h *handler) finalize() {
	h.lp.SetCursorVisible(true)
	h.lp.SetCursorShape(loop.BLOCK_CURSOR, true)
}

func (h *handler) on_query_response(key, val string, valid bool) error {
	if !valid {
		return fmt.Errorf("Terminal does not support querying the: %s", key)
	}
	set_float := func(k, v string, dest *float64) error {
		if fs, err := strconv.ParseFloat(v, 64); err == nil {
			*dest = fs
		} else {
			return fmt.Errorf("Invalid response from terminal to %s query: %#v", k, v)
		}
		return nil
	}
	switch key {
	case "font_size":
		if err := set_float(key, val, &h.text_style.font_sz); err != nil {
			return err
		}
	case "dpi_x":
		if err := set_float(key, val, &h.text_style.dpi_x); err != nil {
			return err
		}
	case "dpi_y":
		if err := set_float(key, val, &h.text_style.dpi_y); err != nil {
			return err
		}
	case "foreground":
		h.text_style.foreground = val
	case "background":
		h.text_style.background = val
	}
	return nil
}

func (h *handler) draw_screen() (err error) {
	h.render_count++
	h.lp.StartAtomicUpdate()
	defer func() {
		h.mouse_state.UpdateHoveredIds()
		h.mouse_state.ApplyHoverStyles(h.lp)
		h.lp.EndAtomicUpdate()
	}()
	h.lp.ClearScreen()
	h.lp.AllowLineWrapping(false)
	h.mouse_state.ClearCellRegions()
	switch h.state {
	case SCANNING_FAMILIES:
		h.lp.Println("Scanning system for fonts, please wait...")
	case LISTING_FAMILIES:
		return h.draw_listing_screen()
	}
	return
}

func (h *handler) on_wakeup() (err error) {
	if err = h.get_worker_error(); err != nil {
		return
	}
	switch h.state {
	case SCANNING_FAMILIES:
		h.state = LISTING_FAMILIES
		h.family_list.UpdateFamilies(utils.StableSortWithKey(utils.Keys(h.fonts), strings.ToLower))
	case LISTING_FAMILIES:
	}
	return h.draw_screen()
}

func (h *handler) on_mouse_event(event *loop.MouseEvent) (err error) {
	rc := h.render_count
	redraw_needed := false
	if h.mouse_state.UpdateState(event) {
		redraw_needed = true
	}
	if event.Event_type == loop.MOUSE_CLICK && event.Buttons&loop.LEFT_MOUSE_BUTTON != 0 {
		if err = h.mouse_state.ClickHoveredRegions(); err != nil {
			return
		}
	}
	if redraw_needed && rc == h.render_count {
		err = h.draw_screen()
	}
	return
}

func (h *handler) on_key_event(event *loop.KeyEvent) (err error) {
	if event.MatchesPressOrRepeat("ctrl+c") {
		event.Handled = true
		return fmt.Errorf("canceled by user")
	}
	switch h.state {
	case LISTING_FAMILIES:
		return h.handle_listing_key_event(event)
	}
	return
}

func (h *handler) on_text(text string, from_key_event bool, in_bracketed_paste bool) (err error) {
	switch h.state {
	case LISTING_FAMILIES:
		return h.handle_listing_text(text, from_key_event, in_bracketed_paste)
	}
	return
}

// }}}

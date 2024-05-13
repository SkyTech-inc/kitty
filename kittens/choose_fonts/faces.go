package choose_fonts

import (
	"fmt"
	"kitty/tools/tui/loop"
	"kitty/tools/utils"
	"math"
	"sync"
)

var _ = fmt.Print

type faces_settings struct {
	font_family, bold_font, italic_font, bold_italic_font string
}

type faces_preview_key struct {
	settings      faces_settings
	width, height int
}

type faces struct {
	handler *handler

	family              string
	settings            faces_settings
	preview_cache       map[faces_preview_key]map[string]string
	preview_cache_mutex sync.Mutex
}

func (self *faces) draw_screen() (err error) {
	lp := self.handler.lp
	lp.SetCursorVisible(false)
	sz, _ := lp.ScreenSize()
	styled := lp.SprintStyled
	lines := []string{
		self.handler.format_title(self.family, 0), "",
		fmt.Sprintf("Press %s to select this font, %s to go back to the font list or any of the highlighted keys below to fine-tune the appearance of the individual font styles.", styled("fg=green", "Enter"), styled("fg=red", "Esc")), "",
	}
	_, y, str := self.handler.render_lines.InRectangle(lines, 0, 0, int(sz.WidthCells), int(sz.HeightCells), &self.handler.mouse_state, self.on_click)

	lp.QueueWriteString(str)

	num_lines_per_font := ((int(sz.HeightCells) - y - 1) / 4) - 2
	num_lines_needed := int(math.Ceil(100. / float64(sz.WidthCells)))
	num_lines := max(1, min(num_lines_per_font, num_lines_needed))
	key := faces_preview_key{settings: self.settings, width: int(sz.WidthCells * sz.CellWidth), height: int(sz.CellHeight) * num_lines}
	self.preview_cache_mutex.Lock()
	defer self.preview_cache_mutex.Unlock()
	previews := self.preview_cache[key]
	if previews == nil {
		self.preview_cache[key] = make(map[string]string)
		self.preview_cache[key]["in_flight"] = "yes"
		go func() {
			var r map[string]string
			s := key.settings
			self.handler.set_worker_error(kitty_font_backend.query("render_family_samples", map[string]any{
				"text_style": self.handler.text_style, "font_family": s.font_family,
				"bold_font": s.bold_font, "italic_font": s.italic_font, "bold_italic_font": s.bold_italic_font,
				"width": key.width, "height": key.height, "output_dir": self.handler.temp_dir,
			}, &r))
			self.preview_cache_mutex.Lock()
			defer self.preview_cache_mutex.Unlock()
			r["in_flight"] = "no"
			self.preview_cache[key] = r
			self.handler.lp.WakeupMainThread()
		}()
		return
	}
	if previews["in_flight"] == "yes" {
		return
	}

	slot := 0
	d := func(setting, title, setting_val string) {
		if int(sz.HeightCells)-y < num_lines+1 {
			return
		}
		lp.MoveCursorTo(1, y+1)
		lp.QueueWriteString(title + fmt.Sprintf(" (%s %s)", setting, setting_val))
		y += 1
		lp.MoveCursorTo(1, y+1)
		self.handler.graphics_manager.display_image(slot, previews[setting], key.width, key.height)
		slot++
		y += num_lines + 1
	}
	d(`font_family`, styled("fg=magenta bold", "R")+`egular`, key.settings.font_family)
	d(`bold_font`, styled("fg=magenta bold", "B")+`old`, key.settings.bold_font)
	d(`italic_font`, styled("fg=magenta bold", "I")+`talic`, key.settings.italic_font)
	d(`bold_italic_font`, "B"+styled("fg=magenta bold", "o")+`ld-Italic`, key.settings.bold_italic_font)

	return
}

func (self *faces) initialize(h *handler) (err error) {
	self.handler = h
	self.preview_cache = make(map[faces_preview_key]map[string]string)
	return
}

func (self *faces) on_wakeup() error {
	return self.handler.draw_screen()
}

func (self *faces) on_click(id string) (err error) {
	return
}

func (self *faces) on_key_event(event *loop.KeyEvent) (err error) {
	if event.MatchesPressOrRepeat("esc") {
		event.Handled = true
		self.handler.current_pane = &self.handler.listing
		return self.handler.draw_screen()
	}
	return
}

func (self *faces) on_text(text string, from_key_event bool, in_bracketed_paste bool) (err error) {
	return
}

func (self *faces) on_enter(family string) error {
	if family != "" {
		self.family = family
		r := self.handler.listing.resolved_faces_from_kitty_conf
		d := func(conf ResolvedFace, setting *string, defval string) {
			*setting = utils.IfElse(family == conf.Family, conf.Spec, defval)
		}
		d(r.Font_family, &self.settings.font_family, family)
		d(r.Bold_font, &self.settings.bold_font, "auto")
		d(r.Italic_font, &self.settings.italic_font, "auto")
		d(r.Bold_italic_font, &self.settings.bold_italic_font, "auto")
	}
	self.handler.current_pane = self
	return self.handler.draw_screen()
}

package webapp

import (
	"fmt"
	"html/template"
)

// svgIcons holds the inner markup of the dashboard's 24x24 SVG icons.
var svgIcons = map[string]string{
	"account_balance": `<path d="M3 20h18"/><path d="M6 20V9M12 20V4M18 20v-7"/>`,
	"upload_file":     `<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/><path d="M12 12v6"/><path d="M9.5 15.5 12 13l2.5 2.5"/>`,
	"description":     `<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/><path d="M8 13h8M8 17h8"/>`,
	"download":        `<path d="M12 3v12m0 0-4-4m4 4 4-4"/><path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/>`,
	"dashboard":       `<rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/>`,
	"table_rows":      `<rect x="3" y="4" width="18" height="16" rx="1"/><path d="M3 9h18M3 14h18"/>`,
	"fact_check":      `<rect x="5" y="4" width="14" height="17" rx="2"/><path d="M9 3.5h6"/><path d="M8.5 12.5l1.8 1.8L15 10"/>`,
	"file_download":   `<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/><path d="M12 11v6"/><path d="M9.5 14.5 12 17l2.5-2.5"/>`,
	"credit_card":     `<rect x="2" y="5" width="20" height="14" rx="2"/><path d="M2 10h20"/><path d="M6 15h4"/>`,
	"cloud_upload":    `<path d="M17.5 19a4.5 4.5 0 0 0 0-9 6 6 0 0 0-11.6 1.7A4 4 0 0 0 6.5 19h11z"/><path d="M12 12v7"/><path d="M9.5 14.5 12 12l2.5 2.5"/>`,
	"bolt":            `<path d="M13 2 4 14h6l-1 8 9-12h-6l1-8z"/>`,
	"error":           `<circle cx="12" cy="12" r="9"/><path d="M12 8v5"/><path d="M12 16h.01"/>`,
	"chevron_left":    `<path d="M15 18l-6-6 6-6"/>`,
	"chevron_right":   `<path d="M9 6l6 6-6 6"/>`,
	"task_alt":        `<circle cx="12" cy="12" r="9"/><path d="M8.5 12.5l2.5 2.5 5-5.5"/>`,
	"receipt_long":    `<path d="M6 2h12v20l-2-1.5L14 22l-2-1.5L10 22l-2-1.5L6 22z"/><path d="M9 7h6M9 11h6M9 15h4"/>`,
	"broken_image":    `<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 16l4.5-4.5a2 2 0 0 1 2.8 0L15 16"/><path d="M14 13l1.5-1.5a2 2 0 0 1 2.8 0L21 14"/><path d="M4 4l16 16"/>`,
	"check_circle":    `<circle cx="12" cy="12" r="9"/><path d="M8 12.5l2.5 2.5L16 9"/>`,
	"warning":         `<path d="M12 4 2 20h20L12 4z"/><path d="M12 10v5"/><path d="M12 18h.01"/>`,
	"category":        `<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><circle cx="17.5" cy="17.5" r="3.5"/>`,
	"payments":        `<rect x="2" y="6" width="20" height="12" rx="2"/><circle cx="12" cy="12" r="3"/><path d="M6 9v.01M18 15v.01"/>`,
	"rule":            `<path d="M4 4h16v16H4z"/><path d="M8 9h.01M8 13h.01M8 17h.01"/><path d="M12 9h4M12 13h4M12 17h4"/>`,
	"touch_app":       `<path d="M4 4l7 16 2-7 7-2z"/>`,
	"search_off":      `<circle cx="10" cy="10" r="6"/><path d="M20 20l-4.35-4.35"/><path d="M4 4l16 16"/>`,
	"light_mode":      `<circle cx="12" cy="12" r="4"/><path d="M12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1"/>`,
	"dark_mode":       `<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/>`,
	"auto_mode":       `<circle cx="12" cy="12" r="8"/><path d="M12 4a8 8 0 0 1 0 16z" fill="currentColor" stroke="none"/>`,
	"debit":           `<path d="M12 5v14"/><path d="M6 13l6 6 6-6"/>`,
	"credit":          `<path d="M12 19V5"/><path d="M6 11l6-6 6 6"/>`,
	"scale":           `<path d="M12 4v16"/><path d="M5 8h14"/><path d="M5 8 2 15h6L5 8z"/><path d="M19 8l-3 7h6l-3-7z"/>`,
	"layers":          `<path d="M12 3 3 8l9 5 9-5-9-5z"/><path d="M3 13l9 5 9-5"/>`,
	"filter":          `<path d="M3 5h18l-7 8v6l-4 2v-8L3 5z"/>`,
	"columns":         `<rect x="3" y="4" width="18" height="16" rx="1.5"/><path d="M9 4v16M15 4v16"/>`,
	"timeline":        `<path d="M3 12h4l3-7 4 14 3-7h4"/>`,
}

// iconHTML renders a named icon as an inline SVG element, marked
// aria-hidden: every icon in this UI sits beside its own text label, so
// announcing it again would only add noise. px defaults to 18.
func iconHTML(name string, px ...int) template.HTML {
	size := 18
	if len(px) > 0 {
		size = px[0]
	}
	body, ok := svgIcons[name]
	if !ok {
		return ""
	}
	return template.HTML(fmt.Sprintf(
		`<svg class="icon" aria-hidden="true" width="%d" height="%d" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">%s</svg>`,
		size, size, body,
	))
}

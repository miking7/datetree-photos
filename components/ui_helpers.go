package components

const buttonBase = "inline-flex items-center justify-center gap-2 rounded-lg px-4 py-2 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/40 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas disabled:cursor-not-allowed disabled:opacity-50"

func buttonClasses(variant string) string {
	switch variant {
	case "primary":
		return buttonBase + " bg-primary text-white hover:bg-primary-hover"
	case "secondary":
		return buttonBase + " bg-neutral-soft text-secondary hover:bg-border"
	case "inverted":
		return buttonBase + " bg-secondary text-white hover:bg-secondary-hover"
	case "outlined":
		return buttonBase + " border border-border-strong bg-surface text-secondary hover:bg-neutral-soft"
	case "destructive":
		return buttonBase + " bg-danger text-white hover:bg-danger-hover"
	}
	return buttonBase + " bg-primary text-white hover:bg-primary-hover"
}

const inputClasses = "w-full rounded-lg border border-border-strong bg-surface px-3 py-2 text-sm text-secondary placeholder:text-neutral focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/30 disabled:cursor-not-allowed disabled:opacity-50"

const checkboxClasses = "h-4 w-4 rounded border-border-strong text-primary focus:ring-2 focus:ring-primary/30 disabled:cursor-not-allowed disabled:opacity-50"

// rowTintClass picks the per-row background tint based on the row's status.
// Selection (row-selected) overrides this via !important in app.src.css.
func rowTintClass(r PreviewRow) string {
	switch {
	case r.HasError:
		return "row-error"
	case r.Conflict:
		return "row-conflict"
	}
	return ""
}

func pillClasses(tone string) string {
	const base = "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium"
	switch tone {
	case "conflict":
		return base + " bg-tertiary-soft text-tertiary"
	case "error":
		return base + " bg-danger-soft text-danger"
	case "info":
		return base + " bg-neutral-soft text-neutral-strong"
	case "ok":
		return base + " bg-neutral-soft text-neutral"
	}
	return base + " bg-neutral-soft text-neutral"
}

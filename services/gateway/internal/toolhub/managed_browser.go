package toolhub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
)

type managedBrowserWindowRegistry struct {
	mu      sync.Mutex
	windows map[string]bool
}

func (h *ToolHub) OpenManagedBrowserWindow(ctx context.Context, ownerID, windowID, targetURL string) error {
	if h == nil || h.browser == nil || !h.cfg.Tools.BrowserAutomation.Enabled {
		return browserautomation.ErrDisabled
	}
	args, key, err := managedBrowserWindowArgs(ownerID, windowID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(targetURL) == "" {
		return errors.New("managed browser target URL is required")
	}
	args["url"] = targetURL
	args["require_visible_environment"] = true

	registry := h.managedBrowserWindows
	if registry == nil {
		return errors.New("managed browser registry is unavailable")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	health, err := h.browser.Health(ctx, args)
	if err != nil {
		return err
	}
	if output, ok := health.Output.(map[string]any); ok && output["ok"] == false {
		return fmt.Errorf("visible Chromium is unavailable: %s", firstString(output, "error", "reason", "status"))
	}
	if !registry.windows[key] {
		_, err = h.browser.Call(ctx, "browser.open", args)
	} else {
		var tabs browserautomation.Result
		tabs, err = h.browser.Call(ctx, "browser.list_tabs", args)
		if err == nil {
			if pageID := selectedManagedBrowserPage(tabs.Pages); pageID != "" {
				args["page_id"] = pageID
				_, err = h.browser.Call(ctx, "browser.navigate", args)
			} else {
				_, err = h.browser.Call(ctx, "browser.open", args)
			}
		}
	}
	if err != nil {
		return err
	}
	registry.windows[key] = true
	return nil
}

func (h *ToolHub) CloseManagedBrowserWindow(ctx context.Context, ownerID, windowID string) error {
	_ = ctx
	if h == nil || h.browser == nil {
		return nil
	}
	args, key, err := managedBrowserWindowArgs(ownerID, windowID)
	if err != nil {
		return err
	}
	registry := h.managedBrowserWindows
	if registry == nil {
		return nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if !registry.windows[key] {
		return nil
	}
	releaser, ok := h.browser.(browserautomation.SessionReleaser)
	if !ok {
		return errors.New("browser adapter cannot release a managed session")
	}
	if err := releaser.ReleaseSession(args); err != nil {
		return err
	}
	delete(registry.windows, key)
	return nil
}

func managedBrowserWindowArgs(ownerID, windowID string) (map[string]any, string, error) {
	ownerID = strings.TrimSpace(ownerID)
	windowID = strings.TrimSpace(windowID)
	if ownerID == "" || windowID == "" {
		return nil, "", errors.New("managed browser owner and window id are required")
	}
	key := ownerID + "\x00" + windowID
	return map[string]any{
		"browser_mode":       "collaborative",
		"presentation":       "visible",
		"surface_visible":    true,
		"visible_browser":    true,
		"owner_id":           ownerID,
		"browser_profile_id": "managed-" + windowID,
	}, key, nil
}

func selectedManagedBrowserPage(pages []any) string {
	fallback := ""
	for _, raw := range pages {
		page, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		pageID := strings.TrimSpace(fmt.Sprint(page["page_id"]))
		if pageID == "" || pageID == "<nil>" {
			continue
		}
		if fallback == "" {
			fallback = pageID
		}
		if selected, _ := page["selected"].(bool); selected {
			return pageID
		}
	}
	return fallback
}

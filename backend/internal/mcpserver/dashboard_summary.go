package mcpserver

import "github.com/olegpustovit/moneybag/backend/internal/components/dashboard"

type dashboardSummary struct {
	ID         string             `json:"id"`
	Title      string             `json:"title"`
	Version    int                `json:"version"`
	Status     string             `json:"status"`
	Sources    []string           `json:"sources"`
	Pages      []pageSummary      `json:"pages"`
	Components []componentSummary `json:"components"`
}

type pageSummary struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Components []string `json:"components"`
}

type componentSummary struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title,omitempty"`
	Format string `json:"format,omitempty"`
}

func summarizeDashboard(result dashboard.GetResult) dashboardSummary {
	spec := result.Spec
	out := dashboardSummary{ID: spec.ID, Title: spec.Title, Version: result.Version, Status: result.Status, Sources: []string{}, Pages: []pageSummary{}, Components: []componentSummary{}}
	for name := range spec.Data {
		out.Sources = append(out.Sources, name)
	}
	for _, page := range spec.Pages {
		ps := pageSummary{ID: page.ID, Title: page.Title, Components: []string{}}
		for _, row := range page.Layout {
			for _, placement := range row.Cols {
				ps.Components = append(ps.Components, placement.Component)
			}
		}
		out.Pages = append(out.Pages, ps)
	}
	for _, c := range spec.Components {
		out.Components = append(out.Components, componentSummary{ID: c.ID, Type: c.Type, Title: c.Title, Format: c.Format})
	}
	return out
}

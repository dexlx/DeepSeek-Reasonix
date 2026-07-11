package control

import (
	"context"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/capability"
	"reasonix/internal/config"
)

func (c *Controller) withCapabilityRoute(composed, routeInput string) string {
	if c == nil {
		return composed
	}
	routeInput = strings.TrimSpace(agent.StripTransientUserBlocks(routeInput))
	if routeInput == "" {
		routeInput = strings.TrimSpace(agent.StripTransientUserBlocks(composed))
	}
	if routeInput == "" {
		return composed
	}
	decision := c.routeCapabilities(routeInput)
	// Pass structured decision to the agent via ledger — never re-parse the prompt.
	if c.executor != nil {
		c.executor.SeedCapabilityRoute(decision)
	}
	block := capability.RenderTransientBlock(decision)
	if block == "" {
		return composed
	}
	return block + "\n\n" + composed
}

func (c *Controller) routeCapabilities(routeInput string) capability.RouteDecision {
	tools := c.ToolContractEntries()
	profile := capability.ProfileBalanced
	delivery := false
	if reg := c.mcp.registry(); reg != nil {
		if _, ok := reg.Get("use_capability"); ok {
			profile = capability.ProfileDelivery
			delivery = true
		}
	}
	opts := capability.CatalogOptions{
		Tools:   tools,
		Skills:  c.Skills(),
		Profile: profile,
	}
	if c.pluginCfg != nil {
		opts.Plugins = c.pluginCfg
	}
	if h := c.Host(); h != nil {
		opts.Connected = map[string]bool{}
		for _, n := range h.ServerNames() {
			opts.Connected[n] = true
		}
		opts.Failed = map[string]string{}
		for _, f := range h.Failures() {
			opts.Failed[f.Name] = f.Error
		}
	}
	catalog := capability.BuildCatalog(opts)
	decision := capability.Route(routeInput, catalog.Entries)

	// Semantic routing only in Delivery when no strong require/prefer match.
	if delivery && c.semanticRouter != nil {
		before := len(decision.Candidates)
		strong := false
		for _, cand := range decision.Candidates {
			if cand.Policy == capability.AutoUseRequire || cand.Policy == capability.AutoUsePrefer {
				strong = true
				break
			}
		}
		if !strong {
			decision = c.semanticRouter.RouteSemantic(context.Background(), routeInput, catalog, decision)
			if c.capabilityAudit != nil {
				fallback := len(decision.Candidates) == before
				c.capabilityAudit.RecordRoute(true, fallback)
			}
		} else if c.capabilityAudit != nil {
			c.capabilityAudit.RecordRoute(false, false)
		}
	} else if c.capabilityAudit != nil {
		c.capabilityAudit.RecordRoute(false, false)
	}
	return decision
}

// WireCapabilityRouting attaches Delivery hybrid routing helpers. Safe to call
// with nil semantic router (deterministic only).
func (c *Controller) WireCapabilityRouting(plugins []config.PluginEntry, router *capability.SemanticRouter, audit *capability.Audit) {
	if c == nil {
		return
	}
	c.pluginCfg = append([]config.PluginEntry(nil), plugins...)
	c.semanticRouter = router
	c.capabilityAudit = audit
}

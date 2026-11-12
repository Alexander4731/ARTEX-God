package llm

// FoldUsage consumes the adapter's explicit usage semantics. Wire snapshots use
// per-field maxima; custom providers keep the original incremental contract.
func (ev StreamEvent) FoldUsage(total *Usage) {
	if !ev.CumulativeUsage {
		total.Add(ev.Usage)
		return
	}
	total.InputTokens = max(total.InputTokens, ev.Usage.InputTokens)
	total.OutputTokens = max(total.OutputTokens, ev.Usage.OutputTokens)
	total.CacheReadTokens = max(total.CacheReadTokens, ev.Usage.CacheReadTokens)
	total.CacheWriteTokens = max(total.CacheWriteTokens, ev.Usage.CacheWriteTokens)
}

# ARTEX execution receipts

Based on upstream norma v0.4.3 (the Go module archive), with its original source
notices retained. The upstream module archive does not include a LICENSE file.

The host can attach a context-scoped execution observer. It receives the exact
tool-use ID before dispatch, real handler results independently of stream
consumption, and managed Bash process start/exit events before output cleanup.
Observers may reject dispatch if durable admission fails. Synthetic stream
interruptions are never sent as executor receipts. Sessions still own processes;
this patch does not promise process survival across service restarts.

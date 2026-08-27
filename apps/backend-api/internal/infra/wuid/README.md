# Worker ID Module

Allocates and renews a process-unique 16-bit worker ID from `WUID` or a
database lease. The module owns its migration source, health state, and
shutdown; the application migration runner inspects and executes that source.

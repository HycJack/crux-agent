module github.com/hycjack/crux-plugin

go 1.25.0

// Plugin core: HTTP-RPC 2.0 over stdio per docs/modules/29-crux-plugin.md.
// The root package is pure stdlib (zero external deps).
// The ./kernel subpackage bridges crux-plugin to crux-kernel's fiber lifecycle,
// so it depends on crux-kernel (the base layer). Kernel itself does not depend on plugin.

require github.com/hycjack/crux-kernel v0.0.0

replace github.com/hycjack/crux-kernel => ../crux-kernel

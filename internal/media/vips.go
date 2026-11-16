package media

// #cgo pkg-config: vips
// #include <stdlib.h>
// #include <vips/vips.h>
import "C"

import (
	"log/slog"
	"sync"
	"unsafe"

	vips "github.com/cshum/vipsgen/vips816"
)

// loaders are the only libvips loaders allowed to run. libvips can read
// many more formats, some through large parsers that aren't meant for
// untrusted files.
var loaders = []string{
	"VipsForeignLoadJpeg",
	"VipsForeignLoadPng",
	"VipsForeignLoadWebp",
	"VipsForeignLoadNsgif",
	"VipsForeignLoadHeif",
}

var startOnce sync.Once

// startVips starts libvips and blocks every loader but loaders.
func startVips() {
	startOnce.Do(func() {
		vips.SetLogging(func(domain string, level vips.LogLevel, msg string) {
			slog.Warn("libvips", "domain", domain, "msg", msg)
		}, vips.LogLevelWarning)
		vips.Startup(&vips.Config{ConcurrencyLevel: 0, MaxCacheFiles: 0, MaxCacheMem: 0, MaxCacheSize: 0})
		block("VipsForeignLoad", true)
		for _, l := range loaders {
			block(l, false)
		}
	})
}

func block(class string, state bool) {
	name := C.CString(class)
	defer C.free(unsafe.Pointer(name))
	b := C.gboolean(C.FALSE)
	if state {
		b = C.TRUE
	}
	C.vips_operation_block_set(name, b)
}

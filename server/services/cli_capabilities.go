package services

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// The deck and the CLI ship separately, so the deck cannot assume the CLI it spawns
// understands every flag the deck knows about. A flag the CLI doesn't recognize is not
// ignored — the process exits immediately with "unknown option", which from the deck's
// side looks like a session that starts and vanishes.
//
// This probes `<bin> --help` once per binary and reports which long options it lists,
// so optional flags are only passed to a CLI that actually has them. Protocol flags
// (-p, --input-format, …) are deliberately NOT gated: a CLI missing those is too old
// to drive at all, and silently dropping them would produce a session that runs but
// speaks the wrong language.
var (
	flagCacheMu sync.Mutex
	flagCache   = map[string]map[string]bool{}
)

var longFlag = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// supportedFlags returns the long options `bin --help` advertises.
//
// A nil result means "couldn't tell" — the caller then passes everything, which is the
// behaviour that existed before this probe. Guessing "unsupported" on a failed probe
// would silently disable working features, which is the worse error: an unsupported
// flag now announces itself loudly (see the driver's start-up death check), while a
// silently dropped one just makes the deck quietly stop honouring its own settings.
func supportedFlags(bin string) map[string]bool {
	if bin == "" {
		return nil
	}
	key := bin
	// An upgrade in place must invalidate the cache, or the deck keeps refusing to use
	// flags the CLI gained an hour ago (and vice-versa after a downgrade).
	if fi, err := os.Stat(bin); err == nil {
		key = bin + "|" + fi.ModTime().UTC().Format(time.RFC3339Nano) + "|" + itoa(fi.Size())
	}

	flagCacheMu.Lock()
	cached, ok := flagCache[key]
	flagCacheMu.Unlock()
	if ok {
		return cached
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
	var flags map[string]bool
	if err == nil || len(out) > 0 {
		flags = make(map[string]bool, 64)
		for _, m := range longFlag.FindAllString(string(out), -1) {
			flags[m] = true
		}
	}
	// An empty parse is as useless as a failed one — treat it as "couldn't tell".
	if len(flags) == 0 {
		flags = nil
	}

	flagCacheMu.Lock()
	flagCache[key] = flags
	flagCacheMu.Unlock()
	return flags
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

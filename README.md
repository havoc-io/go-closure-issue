# go-closure-issue

This is a self-contained demonstration of a Go issue I've encountered. It is
some sort of memory corruption, I think resulting from garbage collection. It is
known to occur on at least `darwin/386` and `darwin/amd64`. I was unable to
reproduce the issue on `windows/386`, though that's certainly not to say that it
can't occur.

The issue is non-deterministic, but very reproducible. To demonstrate the issue,
simply run `go test`. You may need to run a few times to trigger the issue.
Using the `-race` flag is not necessary, but can help in triggering it.

This package is extracted from a larger program. The package is designed to
implement a simple rsync client/server on top of
[https://bitbucket.org/kardianos/rsync](https://bitbucket.org/kardianos/rsync).
Unfortunately I have been unable to produce a more minimal test case, despite
several attempts.

**Neither this package nor the `bitbucket.org/kardianos/rsync` package use cgo
or unsafe.** The test does import the runtime package to get the GOROOT and
GOOS, but these can be hardcoded and the issue still arises.

The problem seems to arise with the callback (defined on line 50 of `server.go`)
that writes rsync operations to the underlying io.Writer. The writer callback is
passed to the rsync engine, but is apparently corrupted after some number of
calls, resulting in a segmentation fault. The rsync package, particularly the
`CreateDelta` method, uses a complicated but seemingly valid set of defers and
closures to wrap up the callback in order to coallesce multiple rsync operations
when possible. I am not by any means an expert on Go internals, but my suspicion
is that the reference to the writer closure is somehow not being detected by the
garbage collector (perhaps due to some optimization (see stack trace below with
optimizations disabled)) and that the closure is being reclaimed. If I add a
`fmt.Println(writer)` after the closure, say on line 51 of `server.go`, or
temporarily retain the closure in the server structure itself until the
`CreateDelta` call completes, the problem does not arise. However, I can also
replace the closure with a write method on the `Server` type itself and this
issue still occurs, which seems to run counter to my theory of the closure being
the source of the issue.

It is worth noting that by defining `GOGC=off`, the error does not occur (which,
combined to the lack of cgo or unsafe usage seems to indict the runtime).
Disabling the new SSA backend seems to make no difference.

This repository includes a few stack trace examples:

- **stack_trace_darwin_386.txt**: The stack trace that appears with
  `GOARCH=386 go test` on a 64-bit OS X system.
- **stack_trace_darwin_amd64.txt**: The stack trace that appears with
  `go test` on a 64-bit OS X system.
- **stack_trace_darwin_amd64_alternate.txt**: This is not a stack trace from
  this demo, but rather from running the test on the rsync package from the
  original program, with compiler optimizations disabled
  (`go test -gcflags="-N"`). I suspect that this stack trace could be reproduced
  with this demo, but it is non-deterministic and I have so far been unable to
  do so. In this case the resulting panic is different, indicating an invalid
  pointer on the heap. None of the packages that this test depends on in the
  large program use cgo or unsafe (it is effectively exactly the same as this
  demo, except that certain portions of this demo exist in other packages). The
  stack trace should still be readable in the context of this demo.

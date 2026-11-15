# 2. Run the program with fake listeners

Date: 2026-11-15

## Decision
probegap builds the program with hooks in `net.Listen` and `http.Server.Serve`, runs it with the container's arguments and environment, and calls the recorded handler in-process. Listeners are fake: they record the address and never accept.

## Why
Static reading of routes misses routers built at run time, prefixes, middleware and listen addresses set by flags or env vars. Calling the real handler sees all of them. Fake listeners mean no port is opened, so the run can't conflict with other processes, needs no privileges for ports below 1024, and can't be reached from outside.

## Cost
Code that runs before the servers start runs for real, so a program that needs a database at startup is refused unless the user gives it stand-in values. Servers that don't use net/http (fasthttp, Fiber) aren't seen. `net.ListenTCP` and other direct listens are not faked.

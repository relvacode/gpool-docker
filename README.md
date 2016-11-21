Distributed Docker engine pooling

[![GoDoc](https://godoc.org/github.com/relvacode/gpool-docker?status.svg)](https://godoc.org/github.com/relvacode/gpool-docker)

For use with [gpool](https://github.com/relvacode/gpool), a concurrent execution toolkit.

Run "jobs" on one or more remote Docker engines using the [go-dockerclient](https://github.com/fsouza/go-dockerclient) library.

## Features

 * Concurrently execute any number of workers on any number of remote Docker engines
 * Built-in health checks for Docker
 * Storage allocation

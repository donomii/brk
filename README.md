[![Build Status](https://travis-ci.org/donomii/brk.svg?branch=master)](https://travis-ci.org/donomii/brk)

# Brk

A UDP library with message retrying

## Summary

This is a UDP message-passing library that is capable of retrying messages that timeout.  It works at the message level, rather than turning the connections into streams.  You send a chunk of bytes to the other end, and sometime later, they receive a buffer of bytes.

## Features

Send a message to any IP address or port without having to _open_ a connection.  Retry failed messages for a period of time.

## Install

	go get -u github.com/donomii/Brk

## Use

```go
package main

import (
	"bufio"
	"flag"
	"os"
	"strconv"
	"fmt"
)

func main() {
	ip := "0.0.0.0"
	port := "6000"

	//NOTE "ip" is the ip address to listen on.  You do not provide the remote server details here!
	//Same for "port"!
	StartRetryServer(ip, port, processor)
}	

```
you also have to provide a processor function, to deal with the communication channels.  Data arrives on _incoming_, and you send messages on _outgoing_

```go
func processor(incoming, outgoing chan UdpMessage) {
	message := []byte("Hello out there!")
	remoteServ := "a.server.somwhere.com"
	remotePort := "6000"
	brk.SendMessage(outgoing, message, remoteServ, remotePort)


	//Read incoming messages and print them to the screen
	go func() {
		for mess := range incoming {
			fmt.Printf("Incoming: %v\n", string(mess.Data))
		}
	}()
}
```

You can send messages at any time after the server starts:

```go
    brk.SendMessage(outgoing, message, remoteServ, remotePort)
```
The ```brk.SendMessage``` function is a convenience wrapper to format the outgoing packet and put it into the _outgoing_ channel.


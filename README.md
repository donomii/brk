# Brick

A UDP library with message retrying

## Summary

This is a UDP message-passing library that is capable of retrying messages that timeout.  It works at the packet level, rather than turning the connections into streams.

## Install

	go get -u github.com/donomii/Brick

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
	SendMessage(outgoing, message, remoteServ, remotePort)


	//Read incoming messages and print them to the screen
	go func() {
		for mess := range incoming {
			fmt.Printf("Incoming: %v\n", string(mess.Data))
		}
	}()
}
```



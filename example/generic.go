package main
//Use:  client.exe -ip 0.0.0.0 192.168.1.10 6000
//
//Listens on 0.0.0.0, sends anything you type to 192.168.1.10
//
// Note that you don't specify the /other/ computer when you start the conenction.  You can send a packet to any server without starting a connection.  Just put the IP address and port in the SendMessage() function.

import (
	"github.com/donomii/brk"
	"bufio"
	"flag"
	"os"
	"strconv"
	"fmt"
)

var remoteServ string
var remotePort int

func processor(incoming, outgoing chan brk.UdpMessage) {

	//message := []byte("Hello out there!")
	//SendMessage(outgoing, message, remoteServ, remotePort)


	//Read incoming messages and print them to the screen
	go func() {
		for mess := range incoming {
			fmt.Printf("Incoming:(%v,%v) %v\n", mess.Address, mess.Port, string(mess.Data))
		}
	}()

	//Read lines from STDIN and send them to the other computer
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(">")
	for {
		text, err := reader.ReadString('\n')
		if err == nil {			
			fmt.Println("\nOutgoing: " + text)
			brk.SendMessage(outgoing, []byte(text), remoteServ, remotePort)
			fmt.Print(">")
		}
	}
}

func main() {
	var ip string
	var port string
	flag.StringVar(&ip, "ip", "127.0.0.1", "listening address")
	flag.StringVar(&port, "port", "1234", "listening port")
	flag.Parse()

	remoteServ = flag.Arg(0)
	remotePort, _ = strconv.Atoi(flag.Arg(1))

	//NOTE "ip" is the ip address to listen on.  You do not provide the remote server details here!
	//Same for "port"!
	brk.StartRetryUdp(ip, port, processor)
}

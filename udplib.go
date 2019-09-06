package brk
//A UDP library with message retrying
//
//This is a UDP message-passing library that is capable of retrying messages that timeout.


import (
	"github.com/ccding/go-stun/stun"
	"fmt"
	"github.com/rcrowley/go-bson"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

type UdpMessage struct {
	Data     []byte
	Address  string
	Port     int
	Sequence int
	Type     string
	Cached   time.Time
}

var sequence int = 2
var Qlength int = 2000

func getSequence() int {
	sequence = sequence + 1
	return sequence
}
func handleUDPConnection(conn *net.UDPConn, incoming, outgoing chan UdpMessage) {

for {
	// here is where you want to do stuff like read or write to client

	buffer := make([]byte, 32768)

	n, addr, err := conn.ReadFromUDP(buffer)

	//fmt.Println("UDP peer : ", addr)
	//fmt.Println("Received from UDP peer :  ", string(buffer[:n]))

	if err != nil {
		log.Fatal(err)
	}
	go func() {
		outbuff := make([]byte, n)
		copy(outbuff, buffer[:n])
		var w UdpMessage
		err = bson.Unmarshal(outbuff, &w)
		if err != nil {
			panic(err)
		}

		//w := w_int.(UdpMessage)
		w.Address = fmt.Sprintf("%v", addr.IP)
		//fmt.Println("Remote address: " + w.Address)
		w.Port = addr.Port
		//fmt.Printf("Remote port: %v\n", w.Port)
		incoming <- w
	}()
}
}

func udpWriter(conn *net.UDPConn, outgoing chan UdpMessage) {
	for mess := range outgoing {
		bson, _ := bson.Marshal(mess)
		//	fmt.Printf("Write sending packet to '%v:%v'\n", mess.Address, mess.Port)
		var straddr string = mess.Address
		addrs, err := net.LookupHost(mess.Address)
		if err == nil {
			straddr = addrs[0]
		} else {
			log.Printf("Could not lookup target address: %v\n", mess.Address)
		}
		ip, _, _ := net.ParseCIDR(straddr + "/32")
		_, err = conn.WriteToUDP(bson, &net.UDPAddr{IP: ip, Port: mess.Port})

		if err != nil {
			log.Println(err)
		}
	}
}

//This is the basic UDP connection.  It does not support retrying, however it does support sending messages to arbitrary addresses.  Please note that the _hostName_ and _portNum_ are the *local* hostname and port number, because this function actually starts a listening server on a local port.  Sending messages is a separate function.
//
//You supply the processor function.  It must run in a loop, reading from _incoming_, and sending messages to _outgoing_ using the _SendMessage_ function.
//
//The incoming data can be read from packet.Data, after you read the packet from _incoming_.
func StartUdp(hostName, portNum string, processor func(incoming, outgoing chan UdpMessage)) (chan UdpMessage, chan UdpMessage) {


	client := stun.NewClient()
	// The default addr (stun.DefaultServerAddr) will be used unless we
	// call SetServerAddr.
	//client.SetServerAddr(*serverAddr)
	// Non verbose mode will be used by default unless we call
	// SetVerbose(true) or SetVVerbose(true).
	//client.SetVerbose(*v || *vv || *vvv)
	//client.SetVVerbose(*vv || *vvv)
	// Discover the NAT and return the result.
	nat, host, err := client.Discover()
	if err != nil {
		fmt.Println(err)
		panic(err)
	}

	fmt.Println("NAT Type:", nat)
	if host != nil {
		fmt.Println("External IP Family:", host.Family())
		fmt.Println("External IP:", host.IP())
		fmt.Println("External Port:", host.Port())
	} else {
		fmt.Println("Could not find host")
	}

	//service := hostName + ":" + portNum
	//service := host.IP() + ":" + fmt.Sprintf("%v", host.Port())
	service := "0.0.0.0:" + fmt.Sprintf("%v", host.Port())
	fmt.Println("Starting server " + service)


	udpAddr, err := net.ResolveUDPAddr("udp4", service)

	if err != nil {
		log.Fatal(err)
	}

	// setup listener for incoming UDP connection
	ln, err := net.ListenUDP("udp", udpAddr)

	if err != nil {
		log.Fatal(err)
	}

	//fmt.Println("UDP server up and listening on port"+portNum)

	defer ln.Close()

	incoming := make(chan UdpMessage, Qlength)
	outgoing := make(chan UdpMessage, Qlength)
	go udpWriter(ln, outgoing)
	go processor(incoming, outgoing)


		// wait for UDP client to connect
		handleUDPConnection(ln, incoming, outgoing)

	return incoming, outgoing
}

// *SendMessage* sends _data_ to _server_, via _outchan_.  You get _outchan_ passed to the _processor_ function, that you provide to StartUdp
func SendMessage(outchan chan UdpMessage, data []byte, server string, port int) {
	outchan <- UdpMessage{data, server, port, getSequence(), "", time.Now()}
}

//This is the UDP connection that retries failed messages.  Otherwise, it works exactly like StartUdp.  It does not detect duplicate messages, you will have to do that yourself.  This is probably the server you want to use.
func StartRetryUdp(hostName, portNum string, processor func(a, b chan UdpMessage)) (chan UdpMessage, chan UdpMessage) {
	seenCache := map[int]bool{}
	cacheLock := sync.Mutex{}
	cache := map[int]UdpMessage{}
	appincoming := make(chan UdpMessage, Qlength)
	appoutgoing := make(chan UdpMessage, Qlength)
	go processor(appincoming, appoutgoing)

	retryProcessor := func(netincoming, netoutgoing chan UdpMessage) {
		go func() {
			for {

				time.Sleep(5 * time.Second)
				
				var keys []int
				cacheLock.Lock()
				for k, _ := range cache {
					//fmt.Printf("Cache has key %v\n", k)
					
					keys = append(keys, k)
					
				}
				cacheLock.Unlock()
				if len(keys) > 0 {
					fmt.Printf("%v messages waiting for retransmission...\n", len(keys))
				}
				for _, k := range keys {
					//fmt.Printf("Checking sequence %v\n", k)
					cacheLock.Lock()
					v, ok := cache[k]
					cacheLock.Unlock()
					if ok {
						//fmt.Printf("Checking sequence val %v\n", k)
						if time.Now().Sub(v.Cached).Seconds() > 2.0 {
							//Retransmit
							fmt.Printf("Retransmitting %v to %v:%v\n", v.Sequence, v.Address, v.Port)
							v.Cached = time.Now()
							cacheLock.Lock()
							cache[v.Sequence] = v
							cacheLock.Unlock()
							//fmt.Printf("Retransmit address is %+v\n", v.Address)
							netoutgoing <- v
						}
					} else {
						//fmt.Printf("Key %v not found in cache\n", k )
					}
				}
				
			}
		}()
		go func() {
			for m := range netincoming {
				if m.Type == "Ack" {
					cacheLock.Lock()
					//fmt.Printf("Deleting queued message %v\n", m.Sequence)
					delete(cache, m.Sequence)
					cacheLock.Unlock()
				} else {
					
	//Reliability testing.  Throw away 50% of incoming packets to force retransmission
	if rand.Float32() < 0.8 {
	fmt.Printf("Acknowledging message %v\n", m.Sequence)
			
		//We must respond with acks before checking the seencache or the peer will retransmit them forever
		netoutgoing <- UdpMessage{[]byte{}, m.Address, m.Port, m.Sequence, "Ack", time.Now()}
		if !seenCache[m.Sequence] {
			seenCache[m.Sequence] = true
			appincoming <- m
		} else {
			fmt.Printf("Discarding duplicate (%v)\n", m.Sequence)
		}
		
	} else {
		fmt.Printf("Discarding message %v to simulate bad connection\n", m.Sequence)
	}
				}
			}
		}()

		for m := range appoutgoing {
			//We add the outgoing packets to a list, and cross them off when the acknowledgement arrives
			//w := RetryWrapper{Sequence: sequence, Data: m}
			m.Sequence = getSequence()
			m.Cached = time.Now()
			cacheLock.Lock()
			cache[m.Sequence] = m
			cacheLock.Unlock()
			netoutgoing <- m
		}
	}
	StartUdp(hostName, portNum, retryProcessor)
	return appincoming, appoutgoing
}

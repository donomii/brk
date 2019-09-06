package main
//Use:  client.exe -ip 0.0.0.0 192.168.1.10 6000
//
//Listens on 0.0.0.0, sends anything you type to 192.168.1.10

import (
	"crypto/sha1"
"log"
	"github.com/rcrowley/go-bson"
	"github.com/donomii/brick"
	"github.com/donomii/hashare"
	"bufio"
	"flag"
	"os"
	"strconv"
	"fmt"
)

var remoteServ string
var remotePort int
var dataDir string = "blocks"
var initIt bool = false
var serverPort = 80
var serverAddress = "0.0.0.0"
var store hashare.SiloStore
var conf *hashare.Config
type UdpMessage struct {
	Type	string
	Data	[]byte
}

type aRoot struct {
		A, B, C, D []byte
	}

func getBlock (key []byte) []byte {
	    defer func() {
		            if r := recover(); r != nil {
				                fmt.Println("Recovered in f", r)
						        }
							    }()
	block := store.GetRecord(key)
	return block
}

func processor(incoming, outgoing chan brick.UdpMessage) {

	//message := []byte("Hello out there!")
	//SendMessage(outgoing, message, remoteServ, remotePort)


	//Read incoming messages and print them to the screen
	go func() {
		for mess := range incoming {
			//fmt.Printf("%+v\n", string(mess.Data))
			var m UdpMessage
			bson.Unmarshal(mess.Data, &m)
			//fmt.Printf("%+v\n", m)
			fmt.Printf("%v\n", m.Type)
			switch m.Type {
				case "WantRoots":
					roots := hashare.GetRoots(store, conf)
					root := roots[0]
					r := aRoot{root[0], root[1], root[2], []byte{}}
					fmt.Printf("Roots %+v\n", r)
					broots, err := bson.Marshal(r)
					if err != nil { panic ( err ) }
					data := UdpMessage{Type : "Roots", Data : broots}
					fmt.Printf("Outgoing Roots %+v\n", data)
					bdata, err := bson.Marshal(data)
					if err != nil { panic ( err ) }
					brick.SendMessage(outgoing, bdata, mess.Address, mess.Port)
				case "WantBlock":
					log.Printf("Want block %v\n", hashare.BytesToHex(m.Data))
					block := getBlock(m.Data)
					//bblock, err := bson.Marshal(block)
					data := UdpMessage{Type : "Block", Data : block}
					bdata, err := bson.Marshal(data)
					if err != nil { panic ( err ) }
					brick.SendMessage(outgoing, bdata, mess.Address, mess.Port)
				case "StoreBlock":
					data := m.Data
					barf := sha1.Sum(m.Data)
					key := barf[:] //FFS
					log.Printf("Got block %v\n", hashare.BytesToHex(key))
					store.InsertRecord(key, data)
				case "StoreRoot":
					data := mess.Data
					var r aRoot
					err := bson.Unmarshal(data, &r)
					if err != nil { panic ( err ) }
					store.InsertRoot(r.D, r.A, string(r.C))
				default:
					log.Printf("Message not understood: %v\n", m.Type)
			}
		}
	}()

	//Read lines from STDIN and send them to the other computer
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(">")
		text, _ := reader.ReadString('\n')
		fmt.Println("\nOutgoing: " + text)
		brick.SendMessage(outgoing, []byte(text), remoteServ, remotePort)
	}
}

func main() {
	log.Println("Start Vort Network File Server")
	log.Printf("Opening repository %v", dataDir)
	var ip string
	var port string
var dataDir string
	flag.StringVar(&ip, "ip", "127.0.0.1", "listening address")
	flag.StringVar(&port, "port", "1234", "listening port")
	flag.StringVar(&dataDir, "repo", "VortFile", "Block directory")
	flag.Parse()


	store = hashare.NewFileStore(dataDir)
	conf = &hashare.Config{
		Store:          nil,
		//Blocksize:      1024 * 1024,
		Blocksize:      256*256,
		UseEncryption:  false,
		UseCompression: false,
		UserName:       "abcd",
		Password:       "efgh",
		EncryptionKey:  []byte("a very very very very secret key"),
	}
	conf = hashare.Init(store, conf)
    if initIt {
        log.Println("Initialising repository")
        hashare.InitRepo(store, conf)
    }

	log.Println("Type: Files") //FIXME make common autodetect routine for repositories, allow any type to be served
	log.Println("Compression:", conf.UseCompression)
	log.Println("Encryption:", conf.UseEncryption)
	log.Println("Blocksize:", conf.Blocksize)
	log.Println("UserName:", conf.UserName)
	log.Println("Password:", conf.Password)

	log.Printf("Listening on %v:%v", serverAddress, serverPort)

	remoteServ = flag.Arg(0)
	remotePort, _ = strconv.Atoi(flag.Arg(1))

	//NOTE "ip" is the ip address to listen on.  You do not provide the remote server details here!
	//Same for "port"!
	brick.StartRetryUdp(ip, port, processor)
}

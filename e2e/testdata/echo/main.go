// Command echo is the TCP service the end-to-end test asks Pier to run: it
// answers each line on 127.0.0.1:$PORT with "echo " and the line.
package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
)

func main() {
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("echo listening on", listener.Addr())
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go func() {
			defer conn.Close()
			lines := bufio.NewScanner(conn)
			for lines.Scan() {
				fmt.Fprintf(conn, "echo %s\n", lines.Text())
			}
		}()
	}
}

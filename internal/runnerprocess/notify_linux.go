package runnerprocess

import (
	"net"
	"os"
	"time"
)

func notify(message string) error {
	address := os.Getenv("NOTIFY_SOCKET")
	if address == "" {
		return nil
	}
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: address, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	_, err = connection.Write([]byte(message))
	return err
}

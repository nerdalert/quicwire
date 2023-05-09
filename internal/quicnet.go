package quicnet

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/songgao/water"
	"go.uber.org/zap"
)

type QuicNet struct {
	localIp         string
	localTunnelPort int
	peerIp          string
	logger          *zap.SugaredLogger
	localIf         *water.Interface
}

func NewQuicNet(logger *zap.SugaredLogger,
	localIp string,
	peerIp string,
	qnetTunnelPort int) (*QuicNet, error) {

	qn := &QuicNet{
		localIp:         localIp,
		localTunnelPort: qnetTunnelPort,
		peerIp:          peerIp,
		logger:          logger,
	}
	return qn, nil
}

func (qn *QuicNet) Start(ctx context.Context, wg *sync.WaitGroup) error {
	qn.logger.Info("QuicNet Starting")
	qn.logger.Info("Trying to create tunnel interface on local host")
	if err := qn.createTunIface(); err != nil {
		return err
	}

        // Start the server
        qn.setupTunnel(wg)
	return nil
}

func (qn *QuicNet) Stop() {
	qn.logger.Info("QuicNet Stop")
}

func (qn *QuicNet) createTunIface() error {
	// Create a TUN interface
	iface, err := water.New(water.Config{DeviceType: water.TUN})
	if err != nil {
		return fmt.Errorf("Failed to create TUN interface: %w", err)
	}
	qn.logger.Infof("TUN interface created: %s", iface.Name())

	// Assign an IP address to the TUN interface
	localIpStr := fmt.Sprintf("%s/24", qn.localIp)
	cmd := exec.Command("ip", "addr", "add", localIpStr, "dev", iface.Name())
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Failed to assign IP address to TUN interface: %w", err)
	}
	qn.logger.Info("IP address assigned to TUN interface")

	// Up the TUN interface
	cmd = exec.Command("ip", "link", "set", "dev", iface.Name(), "up")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Failed to change the state to UP for the TUN interface: %v", err)
	}
	qn.logger.Infof("TUN interface %s is up and running", iface.Name())

	qn.localIf = iface

	return nil
}

func (qn *QuicNet) setupTunnel(wg *sync.WaitGroup) {

	go func() {
		// server mode
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		localipPortStr := fmt.Sprintf("%s:%d", qn.localIp, qn.localTunnelPort)
		s := NewServer(localipPortStr, qn.localIf)
		s.SetHandler(func(c Ctx) error {
			msg := c.String()
			qn.logger.Infof("Client [ %s ] sent a message [ %s ]", c.RemoteAddr().String(), msg)
			return nil
		})
		qn.logger.Fatal(s.StartServer(ctx))
	}()

	go func() {
		_, cancel := context.WithCancel(context.Background())
		defer cancel()
		remotePeerPortStr := fmt.Sprintf("%s:%d", qn.peerIp, qn.localTunnelPort)
		c := NewClient(remotePeerPortStr, qn.localIf)
		//write while loop to call Dial till condition becomes true
		retries := 0
		for {
			err := c.Dial()
			if err != nil {
				qn.logger.Warnf("Failed to dial: %v", err)
				qn.logger.Warnf("Retrying to dial %s", remotePeerPortStr)
				retries++
			} else {
				break
			}
			if retries > 5 {
				break
			}
			time.Sleep(10 * time.Second)
		}

		// Start reading packets from the TUN interface
		packet := make([]byte, 1500)
		for {
			n, err := qn.localIf.Read(packet)
			if err != nil {
				qn.logger.Fatalf("Failed to read packet from TUN interface: %v", err)
				panic(err)
			}

			// Do something with the packet
			qn.logger.Info("Received packet: %v", packet[:n])
			err = c.SendBytes(packet)
			if err != nil {
				fmt.Printf("failed to send client message: %v", err)
			}

		}

	}()

}
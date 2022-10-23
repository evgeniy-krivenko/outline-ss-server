package server

import (
	"container/list"
	"fmt"
	"github.com/evgeniy-krivenko/outline-ss-server/service"
	"github.com/evgeniy-krivenko/outline-ss-server/service/metrics"
	ss "github.com/evgeniy-krivenko/outline-ss-server/shadowsocks"
	"github.com/op/go-logging"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net"
	"time"
)

//var logger *logging.Logger

// 59 seconds is most common timeout for servers that do not respond to invalid requests
const tcpReadTimeout = 59 * time.Second

type SsPort struct {
	tcpService service.TCPService
	udpService service.UDPService
	cipherList service.CipherList
}

type SSServer struct {
	natTimeout  time.Duration
	m           metrics.ShadowsocksMetrics
	replayCache service.ReplayCache
	ports       map[int]*SsPort
	logger      *logging.Logger
}

func NewSSServer(cnf *SSConfig) *SSServer {
	return &SSServer{
		cnf.NatTimeout,
		cnf.Metrics,
		service.NewReplayCache(cnf.ReplayHistory),
		cnf.Ports,
		cnf.Logger,
	}
}

type SSConfig struct {
	NatTimeout    time.Duration
	Metrics       metrics.ShadowsocksMetrics
	ReplayHistory int
	Ports         map[int]*SsPort
	Logger        *logging.Logger
}

func (s *SSServer) startPort(portNum int) error {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{Port: portNum})
	if err != nil {
		return fmt.Errorf("Failed to start TCP on port %v: %v", portNum, err)
	}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{Port: portNum})
	if err != nil {
		return fmt.Errorf("Failed to start UDP on port %v: %v", portNum, err)
	}
	s.logger.Infof("Listening TCP and UDP on port %v", portNum)
	port := &SsPort{cipherList: service.NewCipherList()}
	// TODO: Register initial data metrics at zero.
	port.tcpService = service.NewTCPService(port.cipherList, &s.replayCache, s.m, tcpReadTimeout)
	port.udpService = service.NewUDPService(s.natTimeout, port.cipherList, s.m)
	s.ports[portNum] = port
	go port.tcpService.Serve(listener)
	go port.udpService.Serve(packetConn)
	return nil
}

func (s *SSServer) removePort(portNum int) error {
	port, ok := s.ports[portNum]
	if !ok {
		return fmt.Errorf("Port %v doesn't exist", portNum)
	}
	tcpErr := port.tcpService.Stop()
	udpErr := port.udpService.Stop()
	delete(s.ports, portNum)
	if tcpErr != nil {
		return fmt.Errorf("Failed to close listener on %v: %v", portNum, tcpErr)
	}
	if udpErr != nil {
		return fmt.Errorf("Failed to close packetConn on %v: %v", portNum, udpErr)
	}
	s.logger.Infof("Stopped TCP and UDP on port %v", portNum)
	return nil
}

func (s *SSServer) LoadConfig(filename string) error {
	config, err := readConfig(filename)
	if err != nil {
		return fmt.Errorf("Failed to read config file %v: %v", filename, err)
	}

	portChanges := make(map[int]int)
	portCiphers := make(map[int]*list.List) // Values are *List of *CipherEntry.
	for _, keyConfig := range config.Keys {
		portChanges[keyConfig.Port] = 1
		cipherList, ok := portCiphers[keyConfig.Port]
		if !ok {
			cipherList = list.New()
			portCiphers[keyConfig.Port] = cipherList
		}
		cipher, err := ss.NewCipher(keyConfig.Cipher, keyConfig.Secret)
		if err != nil {
			return fmt.Errorf("Failed to create cipher for key %v: %v", keyConfig.ID, err)
		}
		entry := service.MakeCipherEntry(keyConfig.ID, cipher, keyConfig.Secret)
		cipherList.PushBack(&entry)
	}
	for port := range s.ports {
		portChanges[port] = portChanges[port] - 1
	}
	for portNum, count := range portChanges {
		if count == -1 {
			if err := s.removePort(portNum); err != nil {
				return fmt.Errorf("Failed to remove port %v: %v", portNum, err)
			}
		} else if count == +1 {
			if err := s.startPort(portNum); err != nil {
				return fmt.Errorf("Failed to start port %v: %v", portNum, err)
			}
		}
	}
	for portNum, cipherList := range portCiphers {
		s.ports[portNum].cipherList.Update(cipherList)
	}
	s.logger.Infof("Loaded %v access keys", len(config.Keys))
	s.m.SetNumAccessKeys(len(config.Keys), len(portCiphers))
	return nil
}

type CipherStruct struct {
	ID     string
	Port   int
	Cipher string
	Secret string
}

func (s *SSServer) AddCipher(cs CipherStruct) (int, error) {
	var isPortInit bool
	for port := range s.ports {
		if port == cs.Port {
			isPortInit = true
		}
	}

	if !isPortInit {
		var port = cs.Port
		// iter while starting port
		// TODO Remove iter and create method to check free port
		for err := s.startPort(port); err != nil; {
			s.logger.Errorf("error for starting port: %d", port)
			port++
		}
		cs.Port = port
	}

	cipher, err := ss.NewCipher(cs.Cipher, cs.Secret)
	if err != nil {
		return 0, fmt.Errorf("failed to create cipher for key %v: %v", cs.ID, err)
	}
	entry := service.MakeCipherEntry(cs.ID, cipher, cs.Secret)

	s.ports[cs.Port].cipherList.AddCipher(&entry)

	s.logger.Infof("add cipher with client id %s and port %d", cs.ID, cs.Port)

	return cs.Port, nil
}

func (s *SSServer) RemoveCipher(cs CipherStruct) error {
	ssP, ok := s.ports[cs.Port]
	if !ok {
		return fmt.Errorf("port for remove does not exists in server: %d", cs.Port)
	}
	ssP.cipherList.RemoveCipher(cs.ID)
	l := ssP.cipherList.GetList()
	if l.Len() <= 0 {
		err := s.removePort(cs.Port)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *SSServer) IsCipherExists(cs CipherStruct) bool {
	ssP, ok := s.ports[cs.Port]
	if !ok {
		return false
	}

	return ssP.cipherList.IsCipherExists(cs.ID)
}

// Stop serving on all ports.
func (s *SSServer) Stop() error {
	for portNum := range s.ports {
		if err := s.removePort(portNum); err != nil {
			return err
		}
	}
	return nil
}

type Config struct {
	Keys []struct {
		ID     string
		Port   int
		Cipher string
		Secret string
	}
}

func readConfig(filename string) (*Config, error) {
	config := Config{}
	configData, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(configData, &config)
	return &config, err
}

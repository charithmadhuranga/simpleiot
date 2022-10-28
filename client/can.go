package client

import (
	"log"
	"net"
	"os/exec"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/simpleiot/simpleiot/data"
	"golang.org/x/sys/unix"

	"github.com/go-daq/canbus"
)

const CID_WBSD = 0xCFE4832

// CanPort represents a CAN socket config
type CanSocket struct {
	ID          string `node:"id"`
	Parent      string `node:"parent"`
	Description string `point:"description"`
	Interface   string `point:"interface"`
	BusSpeed    string `point:"busspeed"`
	TxQueueLen  int    `point: txqueuelen`
}

// CanBusClient is a SIOT client used to communicate on a CAN bus
type CanBusClient struct {
	nc            *nats.Conn
	config        CanSocket
	stop          chan struct{}
	newPoints     chan NewPoints
	newEdgePoints chan NewPoints
	wrSeq         byte
	lastSendStats time.Time
	natsSub       string
}

// NewCanBusClient ...
func NewCanBusClient(nc *nats.Conn, config CanSocket) Client {
	return &CanBusClient{
		nc:            nc,
		config:        config,
		stop:          make(chan struct{}),
		newPoints:     make(chan NewPoints),
		newEdgePoints: make(chan NewPoints),
		wrSeq:         0,
		lastSendStats: time.Time{},
		natsSub:       SubjectNodePoints(config.ID),
	}
}

// Start runs the main logic for this client and blocks until stopped
func (cb *CanBusClient) Start() error {
	log.Println("Starting CAN bus client: ", cb.config.Description)

	socket, err := canbus.New()
	if err != nil {
		log.Println(errors.Wrap(err, "Error creating canbus Socket object"))
	}

	canMsgRx := make(chan canbus.Frame)

	closePort := func() {
		socket.Close()
	}

	listener := func() {
		for {
			frame, err := socket.Recv()
			if err != nil {
				log.Println(errors.Wrap(err, "Error recieving CAN frame"))
			}
			canMsgRx <- frame
		}
	}

	openPort := func() {
		closePort()

		iface, err := net.InterfaceByName(cb.config.Interface)
		if err != nil {
			log.Println(errors.Wrap(err, "Internal CAN bus not found"))
		}
		if iface.Flags&net.FlagUp == 0 {
			// bring up CAN interface
			err = exec.Command("ip", "link", "set", cb.config.Interface, "type",
				"can", "bitrate", cb.config.BusSpeed).Run()
			if err != nil {
				log.Println(errors.Wrap(err, "Error configuring internal CAN interface"))
			}

			err = exec.Command("ip", "link", "set", cb.config.Interface, "up").Run()
			if err != nil {
				log.Println(errors.Wrap(err, "Error bringing up internal can interface"))
			}
		} else {
			// Handle case where interface is already up and bus speed may be wrong
			log.Println("Error bringing up internal CAN interface, already set up.")
		}
		filters := [1]unix.CanFilter{
			{
				Id:   CID_WBSD,
				Mask: (unix.CAN_EFF_MASK),
			},
		}
		socket.SetFilters(filters[:])

		err = socket.Bind(cb.config.Interface)
		if err != nil {
			log.Println(errors.Wrap(err, "Error binding to CAN interface"))
		}
		go listener()
	}

	openPort()

	for {
		select {
		case <-cb.stop:
			log.Println("Stopping CAN bus client: ", cb.config.Description)
			closePort()
			return nil
		case frame := <-canMsgRx:

			// Only processing one CAN ID for initial test
			if frame.ID != CID_WBSD {
				break
			}

			points := make(data.Points, 2)

			points[0].Time = time.Now()
			points[1].Time = time.Now()
			points[0].Key = "FE48-1862-WheelBasedSpeed"
			points[1].Key = "FE48-1864-WheelBasedDirection"
			points[0].Value = 0
			points[1].Value = float64(int(frame.Data[7]))

			// Send the points
			if len(points) > 0 {
				err = SendPoints(cb.nc, cb.natsSub, points, false)
				if err != nil {
					log.Println(errors.Wrap(err, "Error sending points received from CAN bus: "))
				} else {
					log.Println("CAN bus client successfully sent points")
				}
			}

		case pts := <-cb.newPoints:
			for _, p := range pts.Points {
				if p.Type == data.PointTypePort ||
					p.Type == data.PointTypeBaud ||
					p.Type == data.PointTypeDisable {
					break
				}

				if p.Type == data.PointTypeDisable {
					if p.Value == 0 {
						closePort()
					}
				}
			}

			err := data.MergePoints(pts.ID, pts.Points, &cb.config)
			if err != nil {
				log.Println("error merging new points: ", err)
			}

		case pts := <-cb.newEdgePoints:
			err := data.MergeEdgePoints(pts.ID, pts.Parent, pts.Points, &cb.config)
			if err != nil {
				log.Println("error merging new points: ", err)
			}

			// TODO need to send edge points to CAN bus, not implemented yet
		}
	}
}

// Stop sends a signal to the Start function to exit
func (cb *CanBusClient) Stop(err error) {
	close(cb.stop)
}

// Points is called by the Manager when new points for this
// node are received.
func (cb *CanBusClient) Points(nodeID string, points []data.Point) {
	cb.newPoints <- NewPoints{nodeID, "", points}
}

// EdgePoints is called by the Manager when new edge points for this
// node are received.
func (cb *CanBusClient) EdgePoints(nodeID, parentID string, points []data.Point) {
	cb.newEdgePoints <- NewPoints{nodeID, parentID, points}
}

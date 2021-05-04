package api

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"

	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/db"
	"github.com/simpleiot/simpleiot/msg"
	"github.com/simpleiot/simpleiot/nats"
)

// NatsHandler implements the SIOT NATS api
type NatsHandler struct {
	server    string
	Nc        *natsgo.Conn
	db        *db.Db
	authToken string
	lock      sync.Mutex
	updates   map[string]time.Time
}

// NewNatsHandler creates a new NATS client for handling SIOT requests
func NewNatsHandler(db *db.Db, authToken, server string) *NatsHandler {
	log.Println("NATS handler connecting to: ", server)
	return &NatsHandler{
		db:        db,
		authToken: authToken,
		updates:   make(map[string]time.Time),
		server:    server,
	}
}

// Connect to NATS server and set up handlers for things we are interested in
func (nh *NatsHandler) Connect() (*natsgo.Conn, error) {
	nc, err := natsgo.Connect(nh.server,
		natsgo.Timeout(10*time.Second),
		natsgo.PingInterval(60*5*time.Second),
		natsgo.MaxPingsOutstanding(5),
		natsgo.ReconnectBufSize(5*1024*1024),
		natsgo.SetCustomDialer(&net.Dialer{
			KeepAlive: -1,
		}),
		natsgo.Token(nh.authToken),
	)

	if err != nil {
		return nil, err
	}

	nh.Nc = nc

	if _, err := nc.Subscribe("node.*.points", nh.handlePoints); err != nil {
		return nil, fmt.Errorf("Subscribe node points error: %w", err)
	}

	if _, err := nc.Subscribe("node.*", nh.handleNode); err != nil {
		return nil, fmt.Errorf("Subscribe node error: %w", err)
	}

	if _, err := nc.Subscribe("node.*.not", nh.handleNotification); err != nil {
		return nil, fmt.Errorf("Subscribe notification error: %w", err)
	}

	if _, err := nc.Subscribe("node.*.msg", nh.handleMessage); err != nil {
		return nil, fmt.Errorf("Subscribe message error: %w", err)
	}

	return nc, nil
}

func (nh *NatsHandler) setSwUpdateState(id string, state data.SwUpdateState) error {
	p := state.Points()

	return nats.SendPoints(nh.Nc, id, p, false)
}

// StartUpdate starts an update
func (nh *NatsHandler) StartUpdate(id, url string) error {
	nh.lock.Lock()
	defer nh.lock.Unlock()

	if _, ok := nh.updates[id]; ok {
		return fmt.Errorf("Update already in process for dev: %v", id)
	}

	nh.updates[id] = time.Now()

	err := nh.setSwUpdateState(id, data.SwUpdateState{
		Running: true,
	})

	if err != nil {
		delete(nh.updates, id)
		return err
	}

	go func() {
		err := NatsSendFileFromHTTP(nh.Nc, id, url, func(bytesTx int) {
			err := nh.setSwUpdateState(id, data.SwUpdateState{
				Running:     true,
				PercentDone: bytesTx,
			})

			if err != nil {
				log.Println("Error setting update status in DB: ", err)
			}
		})

		state := data.SwUpdateState{
			Running: false,
		}

		if err != nil {
			state.Error = "Error updating software"
			state.PercentDone = 0
		} else {
			state.PercentDone = 100
		}

		nh.lock.Lock()
		delete(nh.updates, id)
		nh.lock.Unlock()

		err = nh.setSwUpdateState(id, state)
		if err != nil {
			log.Println("Error setting sw update state: ", err)
		}
	}()

	return nil
}

// FIXME consider moving this to db package and then unexporting the NodePoint method
func (nh *NatsHandler) handlePoints(msg *natsgo.Msg) {
	nodeID, points, err := nats.DecodeNodePointsMsg(msg)

	if err != nil {
		fmt.Printf("Error decoding nats message: %v: %v", msg.Subject, err)
		nh.reply(msg.Reply, errors.New("error decoding node points subject"))
		return
	}

	node, err := nh.db.Node(nodeID)
	desc := node.Desc()

	for _, p := range points {
		err = nh.db.NodePoint(nodeID, p)
		if err != nil {
			// TODO track error stats
			log.Printf("Error writing nodeID (%v) point (%+v) to Db: %v", nodeID, p, err)
			log.Println("msg subject: ", msg.Subject)
			nh.reply(msg.Reply, err)
			return
		}

		err = nh.processPoint(nodeID, nodeID, desc, p)
		if err != nil {
			// TODO track error stats
			log.Println("Error processing point in upstream nodes: ", err)
		}
	}

	nh.reply(msg.Reply, nil)
}

func (nh *NatsHandler) handleNode(msg *natsgo.Msg) {
	chunks := strings.Split(msg.Subject, ".")
	if len(chunks) < 2 {
		log.Println("Error in message subject: ", msg.Subject)
		return
	}

	nodeID := chunks[1]

	node, err := nh.db.Node(nodeID)

	if err != nil {
		log.Printf("NATS: Error getting node %v from db: %v\n", nodeID, err)
		// TODO should we send an error back to requester
	}

	data, err := node.ToPb()

	if err != nil {
		log.Printf("Error pb encoding node: %v\n", err)
		// TODO send error back to client
	}

	err = nh.Nc.Publish(msg.Reply, data)

	if err != nil {
		log.Println("NATS: Error publishing response to node request: ", err)
	}
}

func (nh *NatsHandler) handleNotification(msg *natsgo.Msg) {
	chunks := strings.Split(msg.Subject, ".")
	if len(chunks) < 2 {
		log.Println("Error in message subject: ", msg.Subject)
		return
	}

	nodeID := chunks[1]

	not, err := data.PbDecodeNotification(msg.Data)

	if err != nil {
		log.Println("Error decoding Pb notification: ", err)
		return
	}

	userNodes := []data.NodeEdge{}

	var findUsers func(id string)

	findUsers = func(id string) {
		nodes, err := nh.db.NodeDescendents(id, data.NodeTypeUser, false)
		if err != nil {
			log.Println("Error find user nodes: ", err)
			return
		}

		for _, n := range nodes {
			userNodes = append(userNodes, n)
		}

		// now process upstream nodes
		upIDs, err := nh.db.EdgeUp(id)
		if err != nil {
			log.Println("Error getting upstream nodes: ", err)
			return
		}

		for _, id := range upIDs {
			findUsers(id)
		}
	}

	node, err := nh.db.Node(nodeID)

	if err != nil {
		log.Println("Error getting node: ", nodeID)
		return
	}

	if node.Type == data.NodeTypeUser {
		// if we notify a user node, we only want to message this node, and not walk up the tree
		nodeEdge := node.ToNodeEdge(not.Parent)
		userNodes = append(userNodes, nodeEdge)
	} else {
		findUsers(nodeID)
	}

	for _, userNode := range userNodes {
		user, err := data.NodeToUser(userNode.ToNode())

		if err != nil {
			log.Println("Error converting node to user: ", err)
			continue
		}

		if user.Email != "" || user.Phone != "" {
			msg := data.Message{
				ID:             uuid.New().String(),
				UserID:         user.ID,
				ParentID:       userNode.Parent,
				NotificationID: nodeID,
				Email:          user.Email,
				Phone:          user.Phone,
				Subject:        not.Subject,
				Message:        not.Message,
			}

			data, err := msg.ToPb()

			if err != nil {
				log.Println("Error serializing msg to protobuf: ", err)
				continue
			}

			err = nh.Nc.Publish("node."+user.ID+".msg", data)

			if err != nil {
				log.Println("Error publishing message: ", err)
				continue
			}
		}
	}
}

func (nh *NatsHandler) handleMessage(natsMsg *natsgo.Msg) {
	chunks := strings.Split(natsMsg.Subject, ".")
	if len(chunks) < 2 {
		log.Println("Error in message subject: ", natsMsg.Subject)
		return
	}

	nodeID := chunks[1]

	message, err := data.PbDecodeMessage(natsMsg.Data)

	if err != nil {
		log.Println("Error decoding Pb message: ", err)
		return
	}

	svcNodes := []data.NodeEdge{}

	var findSvcNodes func(string)

	level := 0

	findSvcNodes = func(id string) {
		nodes, err := nh.db.NodeDescendents(id, data.NodeTypeMsgService, false)
		if err != nil {
			log.Println("Error getting svc descendents: ", err)
			return
		}

		svcNodes = append(svcNodes, nodes...)

		// now process upstream nodes
		// if we are at the first level, only process the msg user parent, instead
		// of all user parents. This eliminates duplicate messages when a user is a
		// member of multiple groups which may have different notification services.

		var upIDs []string

		if level == 0 {
			upIDs = []string{message.ParentID}
		} else {
			upIDs, err = nh.db.EdgeUp(id)
			if err != nil {
				log.Println("Error getting upstream nodes: ", err)
				return
			}
		}

		level++

		for _, id := range upIDs {
			findSvcNodes(id)
		}
	}

	findSvcNodes(nodeID)

	svcNodes = data.RemoveDuplicateNodesID(svcNodes)

	for _, svcNode := range svcNodes {
		svc, err := data.NodeToMsgService(svcNode.ToNode())
		if err != nil {
			log.Println("Error converting node to msg service: ", err)
			continue
		}

		if svc.Service == data.PointValueTwilio &&
			message.Phone != "" {
			twilio := msg.NewTwilio(svc.SID, svc.AuthToken, svc.From)

			err := twilio.SendSMS(message.Phone, message.Message)

			if err != nil {
				log.Printf("Error sending SMS to: %v: %v\n",
					message.Phone, err)
			}
		}
	}
}

// used for messages that want an ACK
func (nh *NatsHandler) reply(subject string, err error) {
	if subject == "" {
		// node is not expecting a reply
		return
	}

	reply := ""

	if err != nil {
		reply = err.Error()
	}

	nh.Nc.Publish(subject, []byte(reply))
}

func (nh *NatsHandler) processPoint(currentNodeID, nodeID, nodeDesc string, p data.Point) error {
	// get children and process any rules
	ruleNodes, err := nh.db.NodeDescendents(currentNodeID, data.NodeTypeRule, false)
	if err != nil {
		return err
	}

	for _, ruleNode := range ruleNodes {
		conditionNodes, err := nh.db.NodeDescendents(ruleNode.ID, data.NodeTypeCondition, false)
		if err != nil {
			return err
		}

		actionNodes, err := nh.db.NodeDescendents(ruleNode.ID, data.NodeTypeAction, false)
		if err != nil {
			return err
		}

		rule, err := data.NodeToRule(ruleNode, conditionNodes, actionNodes)

		if err != nil {
			return err
		}

		active, err := ruleProcessPoint(nh.Nc, rule, nodeID, p)

		if err != nil {
			log.Println("Error processing rule point: ", err)
		}

		if active {
			err := nh.ruleRunActions(nh.Nc, rule, nodeID)
			if err != nil {
				log.Println("Error running rule actions: ", err)
			}
		}
	}

	// get database nodes
	dbNodes, err := nh.db.NodeDescendents(currentNodeID, data.NodeTypeDb, false)

	for _, dbNode := range dbNodes {

		influxConfig, err := db.NodeToInfluxConfig(dbNode)

		if err != nil {
			log.Println("Error with influxdb node: ", err)
			continue
		}

		idb := db.NewInflux(influxConfig)

		err = idb.WritePoint(nodeID, nodeDesc, p)

		if err != nil {
			log.Println("Error writing point to influx: ", err)
		}
	}

	upIDs, err := nh.db.EdgeUp(currentNodeID)
	if err != nil {
		return err
	}

	for _, id := range upIDs {

		err = nh.processPoint(id, nodeID, nodeDesc, p)
		if err != nil {
			log.Println("Rules -- error processing upstream node: ", err)
		}
	}

	return nil
}

// ruleProcessPoint runs a point through a rules conditions and and updates condition
// and rule active status. Returns true if point was processed and active is true
func ruleProcessPoint(nc *natsgo.Conn, r *data.Rule, nodeID string, p data.Point) (bool, error) {
	allActive := true
	pointProcessed := false
	for _, c := range r.Conditions {
		if c.NodeID != "" && c.NodeID != nodeID {
			continue
		}

		if c.PointID != "" && c.PointID != p.ID {
			continue
		}

		if c.PointType != "" && c.PointType != p.Type {
			continue
		}

		if c.PointIndex != -1 && c.PointIndex != int(p.Index) {
			continue
		}

		var active bool

		pointProcessed = true

		// conditions match, so check value
		switch c.PointValueType {
		case data.PointValueNumber:
			switch c.Operator {
			case data.PointValueGreaterThan:
				active = p.Value > c.PointValue
			case data.PointValueLessThan:
				active = p.Value < c.PointValue
			case data.PointValueEqual:
				active = p.Value == c.PointValue
			case data.PointValueNotEqual:
				active = p.Value != c.PointValue
			}
		case data.PointValueText:
			switch c.Operator {
			case data.PointValueEqual:
			case data.PointValueNotEqual:
			case data.PointValueContains:
			}
		case data.PointValueOnOff:
			condValue := c.PointValue != 0
			pointValue := p.Value != 0
			active = condValue == pointValue
		}

		if !active {
			allActive = false
		}

		if active != c.Active {
			// update condition
			p := data.Point{
				Type:  data.PointTypeActive,
				Time:  time.Now(),
				Value: data.BoolToFloat(active),
			}

			err := nats.SendPoint(nc, c.ID, p, false)
			if err != nil {
				log.Println("Rule error sending point: ", err)
			}
		}
	}

	if pointProcessed {
		if allActive != r.Active {
			p := data.Point{
				Type:  data.PointTypeActive,
				Time:  time.Now(),
				Value: data.BoolToFloat(allActive),
			}

			err := nats.SendPoint(nc, r.ID, p, false)
			if err != nil {
				log.Println("Rule error sending point: ", err)
			}
		}
	}

	if pointProcessed && allActive {
		return true, nil
	}

	return false, nil
}

// ruleRunActions runs rule actions
func (nh *NatsHandler) ruleRunActions(nc *natsgo.Conn, r *data.Rule, triggerNode string) error {
	for _, a := range r.Actions {
		switch a.Action {
		case data.PointValueActionSetValue:
			if a.NodeID == "" {
				log.Println("Error, node action nodeID must be set, action id: ", a.ID)
			}
			p := data.Point{
				Time:  time.Now(),
				Type:  a.PointType,
				Value: a.PointValue,
				Text:  a.PointTextValue,
			}
			err := nats.SendPoint(nc, a.NodeID, p, false)
			if err != nil {
				log.Println("Error sending rule action point: ", err)
			}
		case data.PointValueActionNotify:
			// get node that fired the rule
			triggerNode, err := nh.db.Node(triggerNode)
			if err != nil {
				return err
			}

			triggerNodeDesc := triggerNode.Desc()

			n := data.Notification{
				ID:         uuid.New().String(),
				SourceNode: a.NodeID,
				Message:    r.Description + " fired at " + triggerNodeDesc,
			}

			d, err := n.ToPb()

			if err != nil {
				return err
			}

			err = nh.Nc.Publish("node."+r.ID+".not", d)

			if err != nil {
				return err
			}
		default:
			log.Println("Uknown rule action: ", a.Action)
		}
	}
	return nil
}

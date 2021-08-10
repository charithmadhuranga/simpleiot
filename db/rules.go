package db

import (
	"log"
	"time"

	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/data"
	"github.com/simpleiot/simpleiot/nats"
)

// ruleProcessPoints runs points through a rules conditions and and updates condition
// and rule active status. Returns true if point was processed and active is true.
// Currently, this function only processes the first point that matches -- this should
// handle all current uses.
func ruleProcessPoints(nc *natsgo.Conn, r *data.Rule, nodeID string, points data.Points) (bool, bool, error) {
	pointsProcessed := false

	for _, p := range points {
		for i, c := range r.Conditions {
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

			switch c.ConditionType {
			case data.PointValuePointValue:

				// conditions match, so check value
				switch c.PointValueType {
				case data.PointValueNumber:
					pointsProcessed = true
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
					pointsProcessed = true
					switch c.Operator {
					case data.PointValueEqual:
					case data.PointValueNotEqual:
					case data.PointValueContains:
					}
				case data.PointValueOnOff:
					pointsProcessed = true
					condValue := c.PointValue != 0
					pointValue := p.Value != 0
					active = condValue == pointValue
				}
			case data.PointValueSchedule:
				if p.Type != data.PointTypeTrigger {
					continue
				}
				pointsProcessed = true
				sched := newSchedule(c.StartTime, c.EndTime, c.Weekdays)

				var err error
				active, err = sched.activeForTime(p.Time)
				if err != nil {
					log.Println("Error parsing schedule time: ", err)
					continue
				}
			}

			if active != c.Active {
				// update condition
				p := data.Point{
					Type:  data.PointTypeActive,
					Time:  time.Now(),
					Value: data.BoolToFloat(active),
				}

				err := nats.SendNodePoint(nc, c.ID, p, false)
				if err != nil {
					log.Println("Rule error sending point: ", err)
				}

				r.Conditions[i].Active = active
			}
		}
	}

	if pointsProcessed {
		allActive := true

		for _, c := range r.Conditions {
			if !c.Active {
				allActive = false
				break
			}
		}

		changed := false

		if allActive != r.Active {
			p := data.Point{
				Type:  data.PointTypeActive,
				Time:  time.Now(),
				Value: data.BoolToFloat(allActive),
			}

			err := nats.SendNodePoint(nc, r.ID, p, false)
			if err != nil {
				log.Println("Rule error sending point: ", err)
			}
			changed = true
		}

		if allActive {
			return true, changed, nil
		}
	}

	return false, false, nil
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
			err := nats.SendNodePoint(nc, a.NodeID, p, false)
			if err != nil {
				log.Println("Error sending rule action point: ", err)
			}
		case data.PointValueActionNotify:
			// get node that fired the rule
			triggerNode, err := nh.db.node(triggerNode)
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

module Pages.Top exposing (Model, Msg, Params, page)

import Api.Auth exposing (Auth)
import Api.Data as Data exposing (Data)
import Api.Node as Node exposing (Node)
import Api.Point as Point exposing (Point)
import Api.Port as Port
import Api.Response exposing (Response)
import Browser.Navigation exposing (Key)
import Components.NodeAction as NodeAction
import Components.NodeCondition as NodeCondition
import Components.NodeDevice as NodeDevice
import Components.NodeGroup as NodeGroup
import Components.NodeMessageService as NodeMessageService
import Components.NodeModbus as NodeModbus
import Components.NodeModbusIO as NodeModbusIO
import Components.NodeRule as NodeRule
import Components.NodeUser as NodeUser
import Components.NodeVariable as NodeVariable
import Element exposing (..)
import Element.Background as Background
import Element.Input as Input
import Http
import List.Extra
import Shared
import Spa.Document exposing (Document)
import Spa.Generated.Route as Route
import Spa.Page as Page exposing (Page)
import Spa.Url exposing (Url)
import Task
import Time
import Tree exposing (Tree)
import Tree.Zipper as Zipper exposing (Zipper)
import UI.Button as Button
import UI.Form as Form
import UI.Icon as Icon
import UI.Style as Style exposing (colors)
import UI.ViewIf exposing (viewIf)
import Utils.Route


page : Page Params Model Msg
page =
    Page.application
        { init = init
        , update = update
        , subscriptions = subscriptions
        , view = view
        , save = save
        , load = load
        }



-- INIT


type alias Params =
    ()


type alias Model =
    { key : Key
    , nodeEdit : Maybe NodeEdit
    , zone : Time.Zone
    , now : Time.Posix
    , nodes : Maybe (Tree NodeView)
    , auth : Auth
    , error : Maybe String
    , nodeOp : NodeOperation
    , copyMove : CopyMove
    , nodeMsg : Maybe NodeMsg
    }


type alias NodeMsg =
    { feID : Int
    , text : String
    , time : Time.Posix
    }


type CopyMove
    = CopyMoveNone
    | Move String String String
    | Copy String String


type NodeOperation
    = OpNone
    | OpNodeToAdd NodeToAdd
    | OpNodeMessage NodeMessage
    | OpNodeDelete Int String String
    | OpNodePaste Int String


type alias NodeView =
    { node : Node
    , feID : Int
    , parentID : String
    , hasChildren : Bool
    , expDetail : Bool
    , expChildren : Bool
    , mod : Bool
    }


type alias NodeEdit =
    { feID : Int
    , points : List Point
    }


type alias NodeToAdd =
    { typ : Maybe String
    , feID : Int
    , parent : String
    }


type alias NodeMessage =
    { feID : Int
    , id : String
    , message : String
    }


defaultModel : Key -> Model
defaultModel key =
    Model
        key
        Nothing
        Time.utc
        (Time.millisToPosix 0)
        Nothing
        { email = "", token = "", isRoot = False }
        Nothing
        OpNone
        CopyMoveNone
        Nothing


init : Shared.Model -> Url Params -> ( Model, Cmd Msg )
init shared { key } =
    let
        model =
            defaultModel key
    in
    case shared.auth of
        Just auth ->
            ( { model | auth = auth }
            , Cmd.batch
                [ Task.perform Zone Time.here
                , Task.perform Tick Time.now
                , Node.list { onResponse = ApiRespList, token = auth.token }
                ]
            )

        Nothing ->
            -- this is not ever used as site is redirected at high levels to sign-in
            ( model
            , Utils.Route.navigate shared.key Route.SignIn
            )



-- UPDATE


type Msg
    = Tick Time.Posix
    | Zone Time.Zone
    | EditNodePoint Int Point
    | ToggleExpChildren Int
    | ToggleExpDetail Int
    | DiscardNodeOp
    | DiscardEdits
    | AddNode Int String
    | MsgNode Int String
    | PasteNode Int String
    | DeleteNode Int String String
    | UpdateMsg String
    | SelectAddNodeType String
    | ApiDelete String String
    | ApiPostPoints String
    | ApiPostAddNode Int
    | ApiPostMoveNode Int String String String
    | ApiPutCopyNode Int String String
    | ApiPostMsgNode
    | ApiRespList (Data (List Node))
    | ApiRespDelete (Data Response)
    | ApiRespPostPoint (Data Response)
    | ApiRespPostAddNode Int (Data Response)
    | ApiRespPostMoveNode Int (Data Response)
    | ApiRespPutCopyNode Int (Data Response)
    | ApiRespPostMsgNode (Data Response)
    | CopyNode Int String String
    | MoveNode Int String String String


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        EditNodePoint feID point ->
            let
                editPoints =
                    case model.nodeEdit of
                        Just ne ->
                            ne.points

                        Nothing ->
                            []
            in
            ( { model
                | nodeEdit =
                    Just
                        { feID = feID
                        , points = Point.updatePoint editPoints point
                        }
              }
            , Cmd.none
            )

        ApiPostPoints id ->
            case model.nodes of
                Just nodes ->
                    case model.nodeEdit of
                        Just edit ->
                            let
                                points =
                                    Point.clearText edit.points

                                -- optimistically update nodes
                                updatedNodes =
                                    Tree.map
                                        (\n ->
                                            if n.node.id == id then
                                                let
                                                    node =
                                                        n.node
                                                in
                                                { n
                                                    | node =
                                                        { node
                                                            | points = Point.updatePoints node.points points
                                                        }
                                                }

                                            else
                                                n
                                        )
                                        nodes
                            in
                            ( { model | nodeEdit = Nothing, nodes = Just updatedNodes }
                            , Node.postPoints
                                { token = model.auth.token
                                , id = id
                                , points = points
                                , onResponse = ApiRespPostPoint
                                }
                            )

                        Nothing ->
                            ( model, Cmd.none )

                Nothing ->
                    ( model, Cmd.none )

        DiscardNodeOp ->
            ( { model | nodeOp = OpNone }, Cmd.none )

        DiscardEdits ->
            ( { model | nodeEdit = Nothing }
            , Cmd.none
            )

        ToggleExpChildren feID ->
            let
                nodes =
                    model.nodes |> Maybe.map (toggleExpChildren feID)
            in
            ( { model | nodes = nodes }, Cmd.none )

        ToggleExpDetail feID ->
            let
                nodes =
                    model.nodes |> Maybe.map (toggleExpDetail feID)
            in
            ( { model | nodes = nodes }, Cmd.none )

        AddNode feID id ->
            ( { model
                | nodeOp = OpNodeToAdd { typ = Nothing, feID = feID, parent = id }
              }
            , Cmd.none
            )

        MsgNode feID id ->
            ( { model
                | nodeOp =
                    OpNodeMessage
                        { id = id
                        , feID = feID
                        , message = ""
                        }
              }
            , Cmd.none
            )

        PasteNode feID id ->
            ( { model | nodeOp = OpNodePaste feID id }, Cmd.none )

        DeleteNode feID id parent ->
            ( { model | nodeOp = OpNodeDelete feID id parent }, Cmd.none )

        UpdateMsg message ->
            case model.nodeOp of
                OpNodeMessage op ->
                    ( { model | nodeOp = OpNodeMessage { op | message = message } }, Cmd.none )

                _ ->
                    ( model, Cmd.none )

        SelectAddNodeType typ ->
            case model.nodeOp of
                OpNodeToAdd add ->
                    ( { model | nodeOp = OpNodeToAdd { add | typ = Just typ } }, Cmd.none )

                _ ->
                    ( model, Cmd.none )

        ApiPostAddNode parent ->
            -- FIXME optimistically update nodes
            case model.nodeOp of
                OpNodeToAdd addNode ->
                    case addNode.typ of
                        Just typ ->
                            ( { model | nodeOp = OpNone }
                            , Node.insert
                                { token = model.auth.token
                                , onResponse = ApiRespPostAddNode parent
                                , node =
                                    { id = ""
                                    , typ = typ
                                    , parent = addNode.parent
                                    , points =
                                        [ Point.newText
                                            ""
                                            Point.typeDescription
                                            "New, please edit"
                                        ]
                                    }
                                }
                            )

                        Nothing ->
                            ( { model | nodeOp = OpNone }, Cmd.none )

                _ ->
                    ( { model | nodeOp = OpNone }, Cmd.none )

        ApiPostMoveNode parent id src dest ->
            ( model
            , Node.move
                { token = model.auth.token
                , id = id
                , oldParent = src
                , newParent = dest
                , onResponse = ApiRespPostMoveNode parent
                }
            )

        ApiPutCopyNode parent id dest ->
            ( model
            , Node.copy
                { token = model.auth.token
                , id = id
                , newParent = dest
                , onResponse = ApiRespPutCopyNode parent
                }
            )

        ApiPostMsgNode ->
            ( model
            , case model.nodeOp of
                OpNodeMessage msgNode ->
                    Node.message
                        { token = model.auth.token
                        , id = msgNode.id
                        , message = msgNode.message
                        , onResponse = ApiRespPostMsgNode
                        }

                _ ->
                    Cmd.none
            )

        ApiDelete id parent ->
            -- optimistically update nodes
            let
                nodes =
                    -- FIXME Tree.filter (\d -> d.id /= id) model.nodes
                    model.nodes
            in
            ( { model | nodes = nodes, nodeOp = OpNone }
            , Node.delete
                { token = model.auth.token
                , id = id
                , parent = parent
                , onResponse = ApiRespDelete
                }
            )

        Zone zone ->
            ( { model | zone = zone }, Cmd.none )

        Tick now ->
            let
                nodeMsg =
                    Maybe.andThen
                        (\m ->
                            let
                                timeMs =
                                    Time.posixToMillis m.time

                                nowMs =
                                    Time.posixToMillis model.now
                            in
                            if nowMs - timeMs > 3000 then
                                Just m

                            else
                                Nothing
                        )
                        model.nodeMsg
            in
            ( { model | now = now, nodeMsg = nodeMsg }
            , updateNodes model
            )

        ApiRespList resp ->
            case resp of
                Data.Success nodes ->
                    let
                        maybeNew =
                            case nodeListToTree nodes of
                                Just tree ->
                                    Just <|
                                        populateHasChildren "" <|
                                            populateFeID <|
                                                sortNodeTree tree

                                Nothing ->
                                    Nothing

                        treeMerged =
                            case ( model.nodes, maybeNew ) of
                                ( Just current, Just new ) ->
                                    Just <| mergeNodeTree current new

                                ( _, Just new ) ->
                                    Just new

                                ( Just current, _ ) ->
                                    Just current

                                _ ->
                                    Nothing
                    in
                    ( { model | nodes = treeMerged }, Cmd.none )

                Data.Failure err ->
                    let
                        signOut =
                            case err of
                                Http.BadStatus code ->
                                    code == 401

                                _ ->
                                    False
                    in
                    if signOut then
                        ( { model | error = Just "Signed Out" }
                        , Utils.Route.navigate model.key Route.SignIn
                        )

                    else
                        ( popError "Error getting nodes" err model
                        , Cmd.none
                        )

                _ ->
                    ( model, Cmd.none )

        ApiRespDelete resp ->
            case resp of
                Data.Success _ ->
                    ( model
                    , updateNodes model
                    )

                Data.Failure err ->
                    ( popError "Error deleting device" err model
                    , updateNodes model
                    )

                _ ->
                    ( model
                    , updateNodes model
                    )

        ApiRespPostPoint resp ->
            case resp of
                Data.Success _ ->
                    ( model
                    , updateNodes model
                    )

                Data.Failure err ->
                    ( popError "Error posting point" err model
                    , updateNodes model
                    )

                _ ->
                    ( model
                    , Cmd.none
                    )

        ApiRespPostAddNode parentFeID resp ->
            case resp of
                Data.Success _ ->
                    ( { model | nodes = model.nodes |> Maybe.map (expChildren parentFeID) }
                    , updateNodes model
                    )

                Data.Failure err ->
                    ( popError "Error adding node" err model
                    , updateNodes model
                    )

                _ ->
                    ( model
                    , updateNodes model
                    )

        ApiRespPostMoveNode parent resp ->
            let
                nodes =
                    model.nodes |> Maybe.map (expChildren parent)
            in
            case resp of
                Data.Success _ ->
                    ( { model | nodeOp = OpNone, copyMove = CopyMoveNone, nodes = nodes }
                    , updateNodes model
                    )

                Data.Failure err ->
                    ( popError "Error moving node" err model
                    , updateNodes model
                    )

                _ ->
                    ( model
                    , updateNodes model
                    )

        ApiRespPutCopyNode parent resp ->
            let
                nodes =
                    model.nodes |> Maybe.map (expChildren parent)
            in
            case resp of
                Data.Success _ ->
                    ( { model | nodeOp = OpNone, copyMove = CopyMoveNone, nodes = nodes }
                    , updateNodes model
                    )

                Data.Failure err ->
                    ( popError "Error copying node" err model
                    , updateNodes model
                    )

                _ ->
                    ( model
                    , updateNodes model
                    )

        ApiRespPostMsgNode resp ->
            case resp of
                Data.Success _ ->
                    ( { model | nodeOp = OpNone }
                    , updateNodes model
                    )

                Data.Failure err ->
                    ( popError "Error messaging node" err model
                    , updateNodes model
                    )

                _ ->
                    ( model
                    , updateNodes model
                    )

        CopyNode feID id desc ->
            ( { model
                | copyMove = Copy id desc
                , nodeMsg =
                    Just
                        { feID = feID
                        , text = "Node copied\nclick paste in destination node"
                        , time = model.now
                        }
              }
            , Port.out <| Port.encodeClipboard id
            )

        MoveNode feID id src desc ->
            ( { model
                | copyMove = Move id src desc
                , nodeMsg =
                    Just
                        { feID = feID
                        , text = "Node queued for move\nclick paste in destination node"
                        , time = model.now
                        }
              }
            , Cmd.none
            )


mergeNodeTree : Tree NodeView -> Tree NodeView -> Tree NodeView
mergeNodeTree current new =
    let
        z =
            Zipper.fromTree current
    in
    Tree.map
        (\n ->
            case
                Zipper.findFromRoot
                    (\o ->
                        o.node.id
                            == n.node.id
                            && o.parentID
                            == n.parentID
                    )
                    z
            of
                Just found ->
                    let
                        l =
                            Zipper.label found
                    in
                    { n
                        | expChildren = l.expChildren
                        , expDetail = l.expDetail
                    }

                Nothing ->
                    n
        )
        new


populateFeID : Tree NodeView -> Tree NodeView
populateFeID tree =
    Tree.indexedMap
        (\i n ->
            { n | feID = i }
        )
        tree


toggleExpChildren : Int -> Tree NodeView -> Tree NodeView
toggleExpChildren feID tree =
    Tree.map
        (\n ->
            if n.feID == feID then
                { n | expChildren = not n.expChildren }

            else
                n
        )
        tree


expChildren : Int -> Tree NodeView -> Tree NodeView
expChildren feID tree =
    Tree.map
        (\n ->
            if n.feID == feID then
                { n | expChildren = True }

            else
                n
        )
        tree


toggleExpDetail : Int -> Tree NodeView -> Tree NodeView
toggleExpDetail feID tree =
    Tree.map
        (\n ->
            if n.feID == feID then
                { n | expDetail = not n.expDetail }

            else
                n
        )
        tree


nodeListToTree : List Node -> Maybe (Tree NodeView)
nodeListToTree nodes =
    List.Extra.find (\n -> n.parent == "") nodes
        |> Maybe.map (populateChildren nodes)



-- populateChildren takes a list of nodes with a parent field and converts
-- this into a tree


populateChildren : List Node -> Node -> Tree NodeView
populateChildren nodes root =
    Zipper.toTree <|
        populateChildrenHelp
            (Zipper.fromTree <| Tree.singleton (nodeToNodeView root))
            nodes


nodeToNodeView : Node -> NodeView
nodeToNodeView node =
    { node = node
    , feID = 0
    , parentID = ""
    , hasChildren = False
    , expDetail = False
    , expChildren = False
    , mod = False
    }


populateChildrenHelp : Zipper NodeView -> List Node -> Zipper NodeView
populateChildrenHelp z nodes =
    case
        Zipper.forward
            (List.foldr
                (\n zCur ->
                    if (Zipper.label zCur).node.id == n.parent then
                        Zipper.mapTree
                            (\t ->
                                Tree.appendChild
                                    (Tree.singleton
                                        (nodeToNodeView n)
                                    )
                                    t
                            )
                            zCur

                    else
                        zCur
                )
                z
                nodes
            )
    of
        Just zMod ->
            populateChildrenHelp zMod nodes

        Nothing ->
            z


populateHasChildren : String -> Tree NodeView -> Tree NodeView
populateHasChildren parentID tree =
    let
        children =
            Tree.children tree

        hasChildren =
            List.length children > 0

        label =
            Tree.label tree

        node =
            { label
                | hasChildren = hasChildren
                , parentID = parentID
            }
    in
    tree
        |> Tree.replaceLabel node
        |> Tree.replaceChildren
            (List.map
                (\c -> populateHasChildren node.node.id c)
                children
            )



-- sortNodeTree recursively sorts the children of the nodes
-- sort by type and then description


sortNodeTree : Tree NodeView -> Tree NodeView
sortNodeTree nodes =
    let
        children =
            Tree.children nodes

        childrenSorted =
            List.sortWith
                (\a b ->
                    let
                        aNode =
                            Tree.label a

                        bNode =
                            Tree.label b

                        aType =
                            aNode.node.typ

                        bType =
                            bNode.node.typ

                        aDesc =
                            String.toLower <| Point.getBestDesc aNode.node.points

                        bDesc =
                            String.toLower <| Point.getBestDesc bNode.node.points
                    in
                    if aType /= bType then
                        compare bType aType

                    else
                        compare bDesc aDesc
                )
                children
    in
    Tree.tree (Tree.label nodes) (List.map sortNodeTree childrenSorted)


popError : String -> Http.Error -> Model -> Model
popError desc err model =
    { model | error = Just (desc ++ ": " ++ Data.errorToString err) }


updateNodes : Model -> Cmd Msg
updateNodes model =
    Node.list { onResponse = ApiRespList, token = model.auth.token }


save : Model -> Shared.Model -> Shared.Model
save model shared =
    { shared
        | error =
            case model.error of
                Nothing ->
                    shared.error

                Just _ ->
                    model.error
        , lastError =
            case model.error of
                Nothing ->
                    shared.lastError

                Just _ ->
                    shared.now
    }


load : Shared.Model -> Model -> ( Model, Cmd Msg )
load shared model =
    ( { model | key = shared.key, error = Nothing }, Cmd.none )


subscriptions : Model -> Sub Msg
subscriptions _ =
    Sub.batch
        [ Time.every 5000 Tick
        ]



-- VIEW


view : Model -> Document Msg
view model =
    { title = "SIOT Nodes"
    , body =
        [ column
            [ width fill, spacing 32 ]
            [ el Style.h2 <| text "Nodes"
            , viewNodes model
            ]
        ]
    }


viewNodes : Model -> Element Msg
viewNodes model =
    column
        [ width fill
        , spacing 24
        ]
    <|
        case model.nodes of
            Just tree ->
                let
                    treeWithEdits =
                        mergeNodeEdit tree model.nodeEdit
                in
                viewNode model Nothing (Tree.label treeWithEdits) 0
                    :: viewNodesHelp 1 model treeWithEdits

            Nothing ->
                [ text "No nodes to display" ]


viewNodesHelp :
    Int
    -> Model
    -> Tree NodeView
    -> List (Element Msg)
viewNodesHelp depth model tree =
    let
        node =
            Tree.label tree

        children =
            if node.expChildren then
                Tree.children tree

            else
                []
    in
    List.foldr
        (\child ret ->
            let
                childNode =
                    Tree.label child
            in
            if shouldDisplay childNode.node.typ then
                ret
                    ++ viewNode model (Just node) childNode depth
                    :: viewNodesHelp (depth + 1) model child

            else
                ret
        )
        []
        children


shouldDisplay : String -> Bool
shouldDisplay typ =
    case typ of
        "user" ->
            True

        "group" ->
            True

        "modbus" ->
            True

        "modbusIo" ->
            True

        "rule" ->
            True

        "condition" ->
            True

        "action" ->
            True

        "device" ->
            True

        "msgService" ->
            True

        "variable" ->
            True

        _ ->
            False


viewNode : Model -> Maybe NodeView -> NodeView -> Int -> Element Msg
viewNode model parent node depth =
    let
        nodeView =
            case node.node.typ of
                "user" ->
                    NodeUser.view

                "group" ->
                    NodeGroup.view

                "modbus" ->
                    NodeModbus.view

                "modbusIo" ->
                    NodeModbusIO.view

                "rule" ->
                    NodeRule.view

                "condition" ->
                    NodeCondition.view

                "action" ->
                    NodeAction.view

                "device" ->
                    NodeDevice.view

                "msgService" ->
                    NodeMessageService.view

                "variable" ->
                    NodeVariable.view

                _ ->
                    viewUnknown

        background =
            if node.expDetail then
                Style.colors.pale

            else
                Style.colors.none

        alignButton =
            el [ alignTop, paddingEach { top = 10, right = 0, left = 0, bottom = 0 } ]

        msg =
            Maybe.andThen
                (\m ->
                    if m.feID == node.feID then
                        Just m.text

                    else
                        Nothing
                )
                model.nodeMsg

        viewNodeOps =
            viewNodeOperations node msg
    in
    el
        [ width fill
        , paddingEach { top = 0, right = 0, bottom = 0, left = depth * 35 }
        , Form.onEnterEsc (ApiPostPoints node.node.id) DiscardNodeOp
        ]
    <|
        row [ spacing 6 ]
            [ alignButton <|
                if not node.hasChildren then
                    Icon.blank

                else if node.expChildren then
                    Button.arrowDown (ToggleExpChildren node.feID)

                else
                    Button.arrowRight (ToggleExpChildren node.feID)
            , alignButton <|
                Button.dot (ToggleExpDetail node.feID)
            , column
                [ spacing 6, padding 6, width fill, Background.color background ]
                [ nodeView
                    { isRoot = model.auth.isRoot
                    , now = model.now
                    , zone = model.zone
                    , modified = node.mod
                    , parent = Maybe.map .node parent
                    , node = node.node
                    , expDetail = node.expDetail
                    , onEditNodePoint = EditNodePoint node.feID
                    }
                , viewIf node.mod <|
                    Form.buttonRow
                        [ Form.button
                            { label = "save"
                            , color = colors.blue
                            , onPress = ApiPostPoints node.node.id
                            }
                        , Form.button
                            { label = "discard"
                            , color = colors.gray
                            , onPress = DiscardEdits
                            }
                        ]
                , if node.expDetail then
                    case model.nodeOp of
                        OpNone ->
                            viewNodeOps

                        OpNodeToAdd add ->
                            if add.feID == node.feID then
                                viewAddNode node add

                            else
                                viewNodeOps

                        OpNodeMessage m ->
                            if m.feID == node.feID then
                                viewMsgNode m

                            else
                                viewNodeOps

                        OpNodeDelete feID id parentId ->
                            if feID == node.feID then
                                viewDeleteNode id parentId

                            else
                                viewNodeOps

                        OpNodePaste feID id ->
                            if feID == node.feID then
                                viewPasteNode feID id model.copyMove

                            else
                                viewNodeOps

                  else
                    Element.none
                ]
            ]


viewUnknown :
    { isRoot : Bool
    , now : Time.Posix
    , zone : Time.Zone
    , modified : Bool
    , expDetail : Bool
    , parent : Maybe Node
    , node : Node
    , onEditNodePoint : Point -> msg
    }
    -> Element msg
viewUnknown o =
    Element.text <| "unknown node type: " ++ o.node.typ


viewNodeOperations : NodeView -> Maybe String -> Element Msg
viewNodeOperations node msg =
    let
        desc =
            Point.getBestDesc node.node.points
    in
    column [ spacing 6 ]
        [ row [ spacing 6 ]
            [ Button.plusCircle (AddNode node.feID node.node.id)
            , Button.message (MsgNode node.feID node.node.id)
            , Button.x (DeleteNode node.feID node.node.id node.node.parent)
            , if node.node.parent /= "" then
                Button.move (MoveNode node.feID node.node.id node.node.parent desc)

              else
                Element.none
            , Button.copy (CopyNode node.feID node.node.id desc)
            , Button.clipboard (PasteNode node.feID node.node.id)
            ]
        , case msg of
            Just m ->
                text m

            Nothing ->
                Element.none
        ]


nodeDescUser : Element Msg
nodeDescUser =
    row [] [ Icon.user, text "User" ]


nodeDescGroup : Element Msg
nodeDescGroup =
    row [] [ Icon.users, text "Group" ]


nodeDescModbus : Element Msg
nodeDescModbus =
    row [] [ Icon.bus, text "Modbus" ]


nodeDescModbusIO : Element Msg
nodeDescModbusIO =
    row [] [ Icon.io, text "Modbus IO" ]


nodeDescRule : Element Msg
nodeDescRule =
    row [] [ Icon.list, text "Rule" ]


nodeDescMsgService : Element Msg
nodeDescMsgService =
    row [] [ Icon.send, text "Messaging Service" ]


nodeDescVariable : Element Msg
nodeDescVariable =
    row [] [ Icon.variable, text "Variable" ]


nodeDescCondition : Element Msg
nodeDescCondition =
    row [] [ Icon.check, text "Condition" ]


nodeDescAction : Element Msg
nodeDescAction =
    row [] [ Icon.trendingUp, text "Action" ]


viewAddNode : NodeView -> NodeToAdd -> Element Msg
viewAddNode parent add =
    column [ spacing 10 ]
        [ Input.radio [ spacing 6 ]
            { onChange = SelectAddNodeType
            , selected = add.typ
            , label = Input.labelAbove [] (el [ padding 12 ] <| text "Select node type to add: ")
            , options =
                []
                    ++ (if parent.node.typ == Node.typeDevice then
                            [ Input.option Node.typeUser nodeDescUser
                            , Input.option Node.typeGroup nodeDescGroup
                            , Input.option Node.typeRule nodeDescRule
                            , Input.option Node.typeModbus nodeDescModbus
                            , Input.option Node.typeMsgService nodeDescMsgService
                            , Input.option Node.typeVariable nodeDescVariable
                            ]

                        else
                            []
                       )
                    ++ (if parent.node.typ == Node.typeGroup then
                            [ Input.option Node.typeUser nodeDescUser
                            , Input.option Node.typeGroup nodeDescGroup
                            , Input.option Node.typeRule nodeDescRule
                            , Input.option Node.typeMsgService nodeDescMsgService
                            , Input.option Node.typeVariable nodeDescVariable
                            ]

                        else
                            []
                       )
                    ++ (if parent.node.typ == Node.typeModbus then
                            [ Input.option Node.typeModbusIO nodeDescModbusIO ]

                        else
                            []
                       )
                    ++ (if parent.node.typ == Node.typeRule then
                            [ Input.option Node.typeCondition nodeDescCondition
                            , Input.option Node.typeAction nodeDescAction
                            ]

                        else
                            []
                       )
            }
        , Form.buttonRow
            [ case add.typ of
                Just _ ->
                    Form.button
                        { label = "add"
                        , color = Style.colors.blue
                        , onPress = ApiPostAddNode parent.feID
                        }

                Nothing ->
                    Element.none
            , Form.button
                { label = "cancel"
                , color = Style.colors.gray
                , onPress = DiscardNodeOp
                }
            ]
        ]


viewMsgNode : NodeMessage -> Element Msg
viewMsgNode msg =
    el [ width fill, paddingEach { top = 10, right = 0, left = 0, bottom = 0 } ] <|
        column
            [ width fill, spacing 32 ]
            [ Input.multiline [ width fill ]
                { onChange = UpdateMsg
                , text = msg.message
                , placeholder = Nothing
                , label = Input.labelAbove [] <| text "Send message to users:"
                , spellcheck = True
                }
            , Form.buttonRow
                [ Form.button
                    { label = "send now"
                    , color = Style.colors.blue
                    , onPress = ApiPostMsgNode
                    }
                , Form.button
                    { label = "cancel"
                    , color = Style.colors.gray
                    , onPress = DiscardNodeOp
                    }
                ]
            ]


viewDeleteNode : String -> String -> Element Msg
viewDeleteNode id parent =
    el [ paddingEach { top = 10, right = 0, left = 0, bottom = 0 } ] <|
        row []
            [ text "Delete this node?"
            , Form.buttonRow
                [ Form.button
                    { label = "yes"
                    , color = colors.red
                    , onPress = ApiDelete id parent
                    }
                , Form.button
                    { label = "no"
                    , color = colors.gray
                    , onPress = DiscardNodeOp
                    }
                ]
            ]


viewPasteNode : Int -> String -> CopyMove -> Element Msg
viewPasteNode feID dest copyMove =
    let
        noButton =
            Form.button
                { label = "no"
                , color = colors.gray
                , onPress = DiscardNodeOp
                }

        yesButton op =
            Form.button
                { label = "yes"
                , color = colors.red
                , onPress = op
                }

        discardButton =
            Form.buttonRow
                [ Form.button
                    { label = "cancel"
                    , color = colors.gray
                    , onPress = DiscardNodeOp
                    }
                ]

        cantCopySelf =
            [ text "Can't copy node to itself"
            , discardButton
            ]
    in
    el [ paddingEach { top = 10, right = 0, left = 0, bottom = 0 } ] <|
        case copyMove of
            CopyMoveNone ->
                row []
                    [ text "Select node to copy/move first"
                    , discardButton
                    ]

            Copy id desc ->
                row [] <|
                    if id == dest then
                        cantCopySelf

                    else
                        [ text <| "Copy " ++ desc ++ " here?"
                        , Form.buttonRow
                            [ yesButton <| ApiPutCopyNode feID id dest
                            , noButton
                            ]
                        ]

            Move id src desc ->
                row [] <|
                    if id == dest then
                        cantCopySelf

                    else
                        [ text <| "Move " ++ desc ++ " here?"
                        , Form.buttonRow
                            [ yesButton <| ApiPostMoveNode feID id src dest
                            , noButton
                            ]
                        ]


mergeNodeEdit : Tree NodeView -> Maybe NodeEdit -> Tree NodeView
mergeNodeEdit nodes nodeEdit =
    case nodeEdit of
        Just edit ->
            Tree.map
                (\n ->
                    if edit.feID == n.feID then
                        let
                            node =
                                n.node
                        in
                        { n
                            | mod = True
                            , node =
                                { node
                                    | points =
                                        Point.updatePoints node.points edit.points
                                }
                        }

                    else
                        { n | mod = False }
                )
                nodes

        Nothing ->
            Tree.map (\n -> { n | mod = False }) nodes

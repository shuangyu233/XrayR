package airgo

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/XrayR-project/XrayR/api"
	"github.com/go-resty/resty/v2"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type APIClient struct {
	client        *resty.Client
	APIHost       string
	NodeID        int
	Key           string
	NodeType      string
	EnableVless   bool
	VlessFlow     string
	SpeedLimit    float64
	DeviceLimit   int
	LocalRuleList []api.DetectRule
	eTags         map[string]string
}

func Show(data any) {
	b, _ := json.Marshal(data)
	fmt.Println("data:", string(b))
}
func New(apiConfig *api.Config) *APIClient {
	client := resty.New()
	client.SetRetryCount(3)
	if apiConfig.Timeout > 0 {
		client.SetTimeout(time.Duration(apiConfig.Timeout) * time.Second)
	} else {
		client.SetTimeout(5 * time.Second)
	}
	client.OnError(func(req *resty.Request, err error) {
		var v *resty.ResponseError
		if errors.As(err, &v) {
			log.Print(v.Err)
		}
	})
	client.SetBaseURL(apiConfig.APIHost)
	// Create Key for each requests
	client.SetQueryParam("key", apiConfig.Key)
	// Read local rule list
	localRuleList := readLocalRuleList(apiConfig.RuleListPath)
	return &APIClient{
		client:        client,
		NodeID:        apiConfig.NodeID,
		Key:           apiConfig.Key,
		APIHost:       apiConfig.APIHost,
		NodeType:      apiConfig.NodeType,
		EnableVless:   apiConfig.EnableVless,
		VlessFlow:     apiConfig.VlessFlow,
		SpeedLimit:    apiConfig.SpeedLimit,
		DeviceLimit:   apiConfig.DeviceLimit,
		LocalRuleList: localRuleList,
		eTags:         make(map[string]string),
	}
}

// readLocalRuleList reads the local rule list file
func readLocalRuleList(path string) (LocalRuleList []api.DetectRule) {
	LocalRuleList = make([]api.DetectRule, 0)
	if path != "" {
		// open the file
		file, err := os.Open(path)
		defer file.Close()
		// handle errors while opening
		if err != nil {
			log.Printf("Error when opening file: %s", err)
			return LocalRuleList
		}
		fileScanner := bufio.NewScanner(file)
		// read line by line
		for fileScanner.Scan() {
			LocalRuleList = append(LocalRuleList, api.DetectRule{
				ID:      -1,
				Pattern: regexp.MustCompile(fileScanner.Text()),
			})
		}
		// handle first encountered error while reading
		if err := fileScanner.Err(); err != nil {
			log.Fatalf("Error while reading file: %s", err)
			return
		}
	}
	return LocalRuleList
}

func (c *APIClient) GetNodeInfo() (*api.NodeInfo, error) {
	path := "/api/public/airgo/node/getNodeInfo"
	res, err := c.client.R().
		SetQueryParams(map[string]string{
			"id": fmt.Sprintf("%d", c.NodeID),
		}).
		SetHeader("If-None-Match", c.eTags["node"]).
		ForceContentType("application/json").
		Get(path)
	// Etag identifier for a specific version of a resource. StatusCode = 304 means no changed
	if res.StatusCode() == 304 {
		return nil, errors.New(api.NodeNotModified)
	}
	// update etag
	if res.Header().Get("Etag") != "" && res.Header().Get("Etag") != c.eTags["node"] {
		c.eTags["node"] = res.Header().Get("Etag")
	}
	var nodeInfoResponse NodeInfoResponse
	err = json.Unmarshal(res.Body(), &nodeInfoResponse)
	nodeInfo, err := c.ParseAirGoNodeInfo(&nodeInfoResponse)
	if err != nil {
		return nil, fmt.Errorf("parse node info failed: %s, \nError: %v", res.String(), err)
	}
	//处理rule
	c.LocalRuleList = []api.DetectRule{}
	for i := range nodeInfoResponse.Access {
		ruleArr := strings.Fields(nodeInfoResponse.Access[i].Route)
		for k, v := range ruleArr {
			n := fmt.Sprintf("%d%d", i+1, k)
			id, _ := strconv.Atoi(n)
			c.LocalRuleList = append(c.LocalRuleList, api.DetectRule{
				ID:      id,
				Pattern: regexp.MustCompile(v),
			})

		}
	}
	return nodeInfo, nil
}
func (c *APIClient) GetUserList() (userList *[]api.UserInfo, err error) {
	path := "/api/public/airgo/user/getUserlist"
	res, err := c.client.R().
		SetQueryParams(map[string]string{
			"id": fmt.Sprintf("%d", c.NodeID),
		}).
		SetHeader("If-None-Match", c.eTags["userlist"]).
		ForceContentType("application/json").
		Get(path)
	// Etag identifier for a specific version of a resource. StatusCode = 304 means no changed
	if res.StatusCode() == 304 {
		return nil, errors.New(api.UserNotModified)
	}
	// update etag
	if res.Header().Get("Etag") != "" && res.Header().Get("Etag") != c.eTags["userlist"] {
		c.eTags["userlist"] = res.Header().Get("Etag")
	}
	var userResponse []UserResponse
	var userInfo []api.UserInfo
	json.Unmarshal(res.Body(), &userResponse)
	var speedLimit uint64
	for _, v := range userResponse {
		if v.NodeSpeedLimit > 0 {
			speedLimit = uint64((v.NodeSpeedLimit * 1000000) / 8)
		} else {
			speedLimit = uint64((c.SpeedLimit * 1000000) / 8)
		}
		userInfo = append(userInfo, api.UserInfo{
			UID:         int(v.ID),
			UUID:        v.UUID,
			Email:       v.UserName,
			Passwd:      v.Passwd,
			SpeedLimit:  speedLimit,
			DeviceLimit: int(v.NodeConnector),
		})
	}
	return &userInfo, nil
}
func (c *APIClient) GetNodeRule() (*[]api.DetectRule, error) {
	ruleList := c.LocalRuleList
	return &ruleList, nil
}

func (c *APIClient) ParseAirGoNodeInfo(n *NodeInfoResponse) (*api.NodeInfo, error) {
	var nodeInfo api.NodeInfo
	var speedLimit uint64
	var enableTLS bool = true
	var enableREALITY bool = false
	var realityConfig = &api.REALITYConfig{}
	var h = make(map[string]any)
	var header json.RawMessage

	if n.NodeSpeedLimit > 0 {
		speedLimit = uint64((n.NodeSpeedLimit * 1000000) / 8)
	} else {
		speedLimit = uint64((c.SpeedLimit * 1000000) / 8)
	}
	if n.Security == "none" || n.Security == "" {
		enableTLS = false
	}
	if n.Security == "reality" {
		enableREALITY = true
		realityConfig = &api.REALITYConfig{
			Dest:             n.Dest,
			ProxyProtocolVer: 0,
			ServerNames:      []string{n.Sni},
			PrivateKey:       n.PrivateKey,
			MinClientVer:     "",
			MaxClientVer:     "",
			MaxTimeDiff:      0,
			ShortIds:         []string{"", "0123456789abcdef"},
		}
	}

	switch n.Protocol {
	case "vless", "Vless":
		nodeInfo = api.NodeInfo{
			EnableVless:       true,
			VlessFlow:         n.VlessFlow,
			NodeType:          c.NodeType,
			NodeID:            c.NodeID,
			Port:              uint32(n.Port),
			SpeedLimit:        speedLimit,
			TransportProtocol: n.Network,
			EnableTLS:         enableTLS,
			Path:              n.Path,
			Host:              n.Host,
			ServiceName:       n.ServiceName,
			EnableREALITY:     enableREALITY,
			REALITYConfig:     realityConfig,
		}
		switch n.Network {
		case "grpc":
		case "ws":
		case "tcp":
			if n.Type == "http" {
				h = map[string]any{
					"type": "http",
					"request": map[string]any{
						"path": []string{
							n.Path,
						},
						"headers": map[string]any{
							"Host": []string{
								n.Host,
							},
						},
					},
				}
				header, _ = json.Marshal(h)
				nodeInfo.Header = header
			}
		}
	case "vmess", "Vmess":
		nodeInfo = api.NodeInfo{
			EnableVless:       false,
			NodeType:          c.NodeType,
			NodeID:            c.NodeID,
			Port:              uint32(n.Port),
			SpeedLimit:        speedLimit,
			AlterID:           0,
			TransportProtocol: n.Network,
			EnableTLS:         enableTLS,
			Path:              n.Path,
			Host:              n.Host,
			CypherMethod:      n.Scy,
			ServiceName:       n.ServiceName,
			EnableREALITY:     enableREALITY,
		}
		switch n.Network {
		case "grpc":
		case "ws":
		case "tcp":
			if n.Type == "http" {
				h = map[string]any{
					"type": "http",
					"request": map[string]any{
						"path": []string{
							n.Path,
						},
						"headers": map[string]any{
							"Host": []string{
								n.Host,
							},
						},
					},
				}
				header, _ = json.Marshal(h)
				nodeInfo.Header = header
			}
		}
	case "Shadowsocks", "shadowsocks":
		nodeInfo = api.NodeInfo{
			NodeType:          c.NodeType,
			NodeID:            c.NodeID,
			Port:              uint32(n.Port),
			SpeedLimit:        speedLimit,
			TransportProtocol: "tcp",
			CypherMethod:      n.Scy,
			ServerKey:         n.ServerKey,
		}
		if n.Type == "http" {
			h = map[string]any{
				"type": "http",
				"request": map[string]any{
					"path": []string{
						n.Path,
					},
					"headers": map[string]any{
						"Host": []string{
							n.Host,
						},
					},
				},
			}
			header, _ = json.Marshal(h)
			nodeInfo.Header = header
		}
	}
	return &nodeInfo, nil
}
func (c *APIClient) ReportNodeStatus(nodeStatus *api.NodeStatus) (err error) {
	path := "/api/public/airgo/node/reportNodeStatus"
	var nodeStatusRequest = NodeStatusRequest{
		ID:     c.NodeID,
		CPU:    nodeStatus.CPU,
		Mem:    nodeStatus.Mem,
		Disk:   nodeStatus.Disk,
		Uptime: nodeStatus.Uptime,
	}
	res, _ := c.client.R().
		SetBody(nodeStatusRequest).
		ForceContentType("application/json").
		Post(path)
	if res.StatusCode() == 200 {
		return nil
	}
	return fmt.Errorf("request %s failed: %s", c.assembleURL(path), err)
}

func (c *APIClient) ReportUserTraffic(userTraffic *[]api.UserTraffic) (err error) {
	path := "/api/public/airgo/user/reportUserTraffic"
	var userTrafficRequest = UserTrafficRequest{
		ID:          c.NodeID,
		UserTraffic: *userTraffic,
	}
	res, _ := c.client.R().
		SetBody(userTrafficRequest).
		ForceContentType("application/json").
		Post(path)
	if res.StatusCode() == 200 {
		return nil
	}
	return fmt.Errorf("request %s failed: %s", c.assembleURL(path), err)

}
func (c *APIClient) ReportNodeOnlineUsers(onlineUserList *[]api.OnlineUser) (err error) {
	var onlineUser = OnlineUser{
		NodeID:      c.NodeID,
		UserNodeMap: make(map[int][]string),
	}
	for _, v := range *onlineUserList {
		onlineUser.UserNodeMap[v.UID] = append(onlineUser.UserNodeMap[v.UID], v.IP)
	}
	path := "/api/public/airgo/user/ReportNodeOnlineUsers"
	res, _ := c.client.R().
		SetBody(onlineUser).
		ForceContentType("application/json").
		Post(path)
	if res.StatusCode() == 200 {
		return nil
	}
	return fmt.Errorf("request %s failed: %s", c.assembleURL(path), err)

}
func (c *APIClient) Describe() api.ClientInfo {
	return api.ClientInfo{}
}

func (c *APIClient) ReportIllegal(detectResultList *[]api.DetectResult) (err error) {
	return nil
}
func (c *APIClient) Debug() {}

func (c *APIClient) assembleURL(path string) string {
	return c.APIHost + path
}

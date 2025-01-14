package comet

import (
	"context"
	"math/rand"
	"time"

	"github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/comet/conf"
	log "github.com/golang/glog"
	"github.com/zhenjl/cityhash"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/keepalive"
)

const (
	minServerHeartbeat = time.Minute * 10
	maxServerHeartbeat = time.Minute * 30
	// grpc options
	grpcInitialWindowSize     = 1 << 24
	grpcInitialConnWindowSize = 1 << 24
	grpcMaxSendMsgSize        = 1 << 24
	grpcMaxCallMsgSize        = 1 << 24
	grpcKeepAliveTime         = time.Second * 10
	grpcKeepAliveTimeout      = time.Second * 3
	grpcBackoffMaxDelay       = time.Second * 3
)

func newLogicClient(c *conf.RPCClient) logic.LogicClient {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.Dial))
	defer cancel()
	conn, err := grpc.DialContext(ctx, "discovery://default/goim.logic",
		[]grpc.DialOption{
			grpc.WithInsecure(),
			grpc.WithInitialWindowSize(grpcInitialWindowSize),
			grpc.WithInitialConnWindowSize(grpcInitialConnWindowSize),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(grpcMaxCallMsgSize)),
			grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(grpcMaxSendMsgSize)),
			grpc.WithBackoffMaxDelay(grpcBackoffMaxDelay),
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                grpcKeepAliveTime,
				Timeout:             grpcKeepAliveTimeout,
				PermitWithoutStream: true,
			}),
			grpc.WithBalancerName(roundrobin.Name),
		}...)
	if err != nil {
		panic(err)
	}
	return logic.NewLogicClient(conn)
}

// Server is comet server.
type Server struct {
	c         *conf.Config
	round     *Round    // accept round store
	buckets   []*Bucket // subkey bucket    房子数量（同样的房间id分布在不同的房子）
	bucketIdx uint32

	serverID  string //c.Env.Host
	rpcClient logic.LogicClient
}

// NewServer returns a new Server.
func NewServer(c *conf.Config) *Server {
	s := &Server{
		c:         c,
		round:     NewRound(c), //tp 读写缓存
		rpcClient: newLogicClient(c.RPCClient),
	}
	// init bucket
	s.buckets = make([]*Bucket, c.Bucket.Size) //生成房子图纸
	s.bucketIdx = uint32(c.Bucket.Size)
	for i := 0; i < c.Bucket.Size; i++ {
		s.buckets[i] = NewBucket(c.Bucket) //生成房子
	}
	s.serverID = c.Env.Host
	go s.onlineproc()
	return s
}

// Buckets return all buckets.
func (s *Server) Buckets() []*Bucket {
	return s.buckets
}

// Bucket get the bucket by subkey.
func (s *Server) Bucket(subKey string) *Bucket {
	idx := cityhash.CityHash32([]byte(subKey), uint32(len(subKey))) % s.bucketIdx //安装城市取房子
	if conf.Conf.Debug {
		log.Infof("%s hit channel bucket index: %d use cityhash", subKey, idx)
	}
	return s.buckets[idx]
}

// RandServerHearbeat rand server heartbeat.
func (s *Server) RandServerHearbeat() time.Duration {
	return (minServerHeartbeat + time.Duration(rand.Int63n(int64(maxServerHeartbeat-minServerHeartbeat))))
}

// Close close the server.
func (s *Server) Close() (err error) {
	return
}

//todo
func (s *Server) onlineproc() {
	for {
		var (
			allRoomsCount map[string]int32
			err           error
		)
		roomCount := make(map[string]int32)
		for _, bucket := range s.buckets { //循环房子
			for roomID, count := range bucket.RoomsCount() { //获取房子下的有在线的房间
				roomCount[roomID] += count
			}
		}
		if allRoomsCount, err = s.RenewOnline(context.Background(), s.serverID, roomCount); err != nil {
			time.Sleep(time.Second)
			continue
		}
		for _, bucket := range s.buckets {
			bucket.UpRoomsCount(allRoomsCount)
		}
		time.Sleep(time.Second * 10)
	}
}

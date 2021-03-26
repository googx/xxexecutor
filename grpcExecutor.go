/*
--------------------------------------------------
 File Name: grpcExecutor.go
 Author: hanxu
 AuthorSite: http://www.googx.top/
 GitSource: https://github.com/googx/linuxShell
 Created Time: 2020-6-28-上午10:54
---------------------说明--------------------------
 使用grpc + protobuf 实现命令执行器
---------------------------------------------------
*/

package xxexecutor

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/googx/fcommons/util/xxbytes"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"github.com/googx/fcommons/service/t2_command"
	"github.com/googx/fcommons/util/xxid"
	"github.com/googx/fcommons/util/xxtime"

	"github.com/googx/xxexecutor/pbgocmd"
)

//go:generate protoc --proto_path=proto/ --proto_path=../protolib/ --go_out=plugins=grpc:pbgo_grpc/ commandMsg.proto

type GrpcCommandExecutor struct {
	Host     string
	Port     uint16
	Log      *zap.Logger
	Debug    bool
	cli      pbgocmd.CommandServiceClient
	connOnce sync.Once
	connErr  error
	// cacheCommandSchema
	mlock   sync.Mutex
	status  ConnStats
	srvInfo map[string][]command.ServiceInfo // 以serviceId为key,多个版本的Service的集合为value
}

type ConnStats byte

const (
	ConnStats_NotInit ConnStats = iota
	ConnStats_Initing
	ConnStats_Ok
	ConnStats_Failed
)

func (e GrpcCommandExecutor) Name() string {
	return fmt.Sprintf("grpcExecutor(%s:%d)", e.Host, e.Port)
}

func (e *GrpcCommandExecutor) init() error {
	if e.cli != nil {
		return nil
	}
	if e.status != ConnStats_NotInit {
		return fmt.Errorf("abnormal status :%d", e.status)
	}
	e.status = ConnStats_Initing
	/*if e.connErr != nil {
		return e.connErr
	}*/
	target := net.JoinHostPort(e.Host, strconv.Itoa(int(e.Port)))
	//e.connOnce.Do(func() {

	clientConn, err := grpc.Dial(target,
		grpc.WithInsecure(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    time.Second * 10,
			Timeout: time.Second * 25,
		}),
	)
	if err != nil {
		e.connErr = err
		return e.connErr
	}
	client := pbgocmd.NewCommandServiceClient(clientConn)
	seq := rand.Int31n(10000)
	if ackMessage, err := client.TestConn(context.Background(), &pbgocmd.SeqMessage{
		Seq:     seq,
		MsgTime: xxtime.CurrentUnixMillis(),
	}); err != nil {
		e.connErr = fmt.Errorf("testconn for target(%s)with err:%v", target, err)
		return e.connErr
	} else if ackMessage.Ack != (seq + 1) {
		e.connErr = fmt.Errorf("wrong testConn response,expect [%d],but accept [%d]", seq+1, ackMessage.Ack)
		return e.connErr
	}
	e.cli = client
	//})
	if e.Log == nil {
		config := zap.NewDevelopmentConfig()
		logger, _ := config.Build()
		e.Log = logger.Named("grpcExecutor")
	}
	return e.connErr
}

const (
	ResultCode_Success = iota
	ResultCode_Unknow
	ResultCode_Connfail
	ResultCode_ExecuteFail
	ResultCode_ReqIdNotMatch
	ResultCode_IncorrectResponse // Incorrect implementation
	ResultCode_ServerErr
)

func getGlobalCallOptions() []grpc.CallOption {
	return []grpc.CallOption{
		grpc.MaxCallRecvMsgSize(xxbytes.MB.Multiply(20).Int()),
		grpc.MaxCallSendMsgSize(xxbytes.MB.Multiply(20).Int()),
	}
}

func (gce *GrpcCommandExecutor) Execute(ctx context.Context, cmd command.Command) (resu *command.CommandResult) {
	fileCommand := command.SimpleFileCommand(cmd, nil)
	fileCommandResult := gce.ExecuteWithFile(ctx, fileCommand)
	return fileCommandResult.GetCommandResult()
}

func (gce *GrpcCommandExecutor) ExecuteWithFile(ctx context.Context, cmd command.FileCommand) (resu command.FileCommandResult) {
	var cmdResult *command.CommandResult
	var (
		respFileOriginal []byte
		respFileInfo     string
	)
	// 设置消息id和 grpc响应的附件处理
	defer func() {
		cmdResult.CommandMetaData = command.NewCommandMetaData(cmd.Id(), map[string]string{
			"Executor": gce.Name(),
		})
		resu = command.FileResult(cmdResult, command.NewByteAccessory(respFileInfo, respFileOriginal))
	}()
	gce.connOnce.Do(func() {
		gce.mlock.Lock()
		defer gce.mlock.Unlock()
		if e := gce.init(); e != nil {
			gce.status = ConnStats_Failed
			return
		}
		gce.status = ConnStats_Ok
	})
	if gce.status != ConnStats_Ok {
		cmdResult = command.FailCmdResult(ResultCode_Connfail, gce.connErr)
		return
	}

	grpcReq := createGrpcCommandRequest(ctx, cmd)
	execStart := time.Now()
	grpcResp, err := gce.cli.Command(ctx, grpcReq, getGlobalCallOptions()...)
	execCost := time.Now().Sub(execStart)
	defer func() {
		zlogField := buildZlogField(grpcReq, cmdResult)
		zlogField = append(zlogField, zap.Duration("CostTime", execCost))
		if cmdResult.IsOk() {
			if gce.Debug {
				zlogField = append(zlogField, zap.ByteString("Params", grpcReq.GetParam()))
				zlogField = append(zlogField, zap.ByteString("Result", grpcResp.GetData()))
			}
			gce.Log.Info("execute rpc successful", zlogField...)
		} else {
			gce.Log.Warn("execute rpc failed", zlogField...)
		}
	}()
	if err != nil {
		if e, ok := status.FromError(err); ok {
			var grpcStatusErr error
			switch e.Code() {
			case codes.DeadlineExceeded:
				grpcStatusErr = fmt.Errorf("(%d):%s", e.Code(), "request timeout")
			case codes.Unknown:
				grpcStatusErr = fmt.Errorf("(%d):%s", e.Code(), "request failed")
			default:
				grpcStatusErr = fmt.Errorf("(%d):%s", e.Code(), e.Message())
			}
			cmdResult = command.FailCmdResult(ResultCode_ExecuteFail, grpcStatusErr)
			return
		}
		cmdResult = command.FailCmdResult(ResultCode_ExecuteFail, err)
		return
	}
	respFileOriginal = grpcResp.GetInSecureData()
	respFileInfo = grpcResp.GetInSecureInfo()
	var grpcRespHead *pbgocmd.MessageHead
	if grpcRespHead = grpcResp.GetCmdHead(); grpcRespHead == nil {
		cmdResult = command.FailCmdResult(ResultCode_IncorrectResponse, fmt.Errorf("invalid response message,missing messageHead"))
		return
	}
	// check response
	if !strings.EqualFold(grpcRespHead.GetId(), grpcReq.GetCmdHead().GetId()) {
		cmdResult = command.FailCmdResult(ResultCode_ReqIdNotMatch, fmt.Errorf("invalid response message,unmatch messageId"))
		return
	}
	if grpcResp.GetCode() != 0 {
		cmdResult = command.FailCmdResult(int(grpcResp.GetCode()), fmt.Errorf(grpcResp.GetMessage()))
		return
	}
	cmdResult = command.OkCmdResult(grpcResp.GetData())
	return
}

func createGrpcCommandRequest(ctx context.Context, cmd command.FileCommand) (resu *pbgocmd.CommandRequest) {
	//if e := gce.init(); e != nil {
	//	resu = command.FailCmdResult(ResultCode_Connfail, e)
	//	return
	//}
	msgId := cmd.Id()
	if len(msgId) == 0 {
		msgId = xxid.NewGuid().IdStr()
	}
	serviceId := strings.TrimSpace(cmd.ServiceId())
	funcId := strings.TrimSpace(cmd.FuncId())
	req := &pbgocmd.CommandRequest{
		CmdHead: &pbgocmd.MessageHead{
			Id:          msgId,
			MsgTypes:    0,
			MsgTime:     time.Now().Unix(),
			MsgLiveTime: 0,
			CallTimeout: 0,
			Sign:        "",
			Extend:      cmd.Head(),
		},
		ServiceId:  serviceId,
		FunctionId: funcId,
		Param:      cmd.CmdMsg(),
	}
	if accessory := cmd.GetAccessory(); accessory != nil && !accessory.IsEmpty() {
		if bs, err := ioutil.ReadAll(accessory.Reader()); err != nil {
			// todo
		} else {
			req.InSecureInfo = accessory.Info()
			req.InSecureData = bs
		}
	}
	return req
}

func buildZlogField(req *pbgocmd.CommandRequest, resu *command.CommandResult) (zlogField []zap.Field) {
	if resu.IsOk() {
		zlogField = build(
			zap.String("ServiceId", req.GetServiceId()),
			zap.String("FuncId", req.GetFunctionId()),
			zap.Bool("Status", true),
		)
	} else {
		zlogField = build(
			zap.String("ServiceId", req.GetServiceId()),
			zap.String("FuncId", req.GetFunctionId()),
			//
			zap.ByteString("Params", req.GetParam()),
			zap.Bool("Status", false),
			zap.Int("error-code", resu.Code),
			zap.String("error-msg", resu.ErrMsg()),
		)
	}
	return zlogField
}
func build(field ...zap.Field) []zap.Field {
	return field
}
func (gce *GrpcCommandExecutor) ListServiceName() (serviceinfos []command.ServiceInfo, err error) {
	if e := gce.init(); e != nil {
		return nil, e
	}
	defer func() {
		gce.mlock.Lock()
		gce.srvInfo = make(map[string][]command.ServiceInfo, len(serviceinfos))
		if len(serviceinfos) > 0 {
			for i, _ := range serviceinfos {
				v := serviceinfos[i]
				infos := gce.srvInfo[v.ServiceId]
				infos = append(infos, v)
				gce.srvInfo[v.ServiceId] = infos
			}
		}
		gce.mlock.Unlock()
	}()
	var cmdlistResp *pbgocmd.ListCmdResponse
	if cmdlistResp, err = gce.cli.ListCommand(context.TODO(), &pbgocmd.ListCmdRequest{}); err != nil {
		return nil, err
	}
	if services := cmdlistResp.Services; services != nil {
		for _, item := range services {
			si := command.ServiceInfo{
				ServiceId:   item.GetSrvId(),
				Version:     item.GetVersion(),
				ServiceName: item.GetName(),
			}
			if cmds := item.GetCmds(); cmds != nil {
				for _, cmd := range cmds {
					si.Funcs = append(si.Funcs, command.ServiceFuncInfo{
						FuncId:   cmd.GetFunctionID(),
						FuncName: cmd.GetName(),
					})
				}
			}
			serviceinfos = append(serviceinfos, si)
		}
	}
	return serviceinfos, nil
}

func (gce *GrpcCommandExecutor) ObtainService(name string) ([]command.ServiceInfo, error) {
	if e := gce.init(); e != nil {
		return nil, e
	}
	gce.mlock.Lock()
	defer gce.mlock.Unlock()
	infos := gce.srvInfo[name]
	return infos, nil
}

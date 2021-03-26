/*
--------------------------------------------------
 File Name: GrpcCommandService.go
 Author: hanxu
 AuthorSite: http://www.googx.top/
 GitSource: https://github.com/googx/linuxShell
 Created Time: 2021/1/29-下午4:06
---------------------说明--------------------------

---------------------------------------------------
*/

package xxexecute

import (
	"context"
	"io/ioutil"
	"time"

	command "github.com/googx/fcommons/service/t2_command"
	"github.com/googx/fcommons/util/xxtime"

	"github.com/googx/xxexecutor/pbgocmd"
)

type GrpcCmdServiceOption func(opt *grpcCmdServiceOption)
type grpcCmdServiceOption struct {
	cmdTimeout      time.Duration // 单次请求最大超时时间
	serviceDeadline time.Time
}

type GrpcCommandService struct {
	opts     *grpcCmdServiceOption
	executor command.FileCommandExecuter
}

func NewGrpcCommandService(executor command.FileCommandExecuter, opts ...GrpcCmdServiceOption) *GrpcCommandService {
	defopt := &grpcCmdServiceOption{
		cmdTimeout:      0,
		serviceDeadline: time.Time{},
	}
	for _, o := range opts {
		o(defopt)
	}
	// todo init and check options
	cmdsrv := &GrpcCommandService{
		opts:     defopt,
		executor: executor,
	}
	return cmdsrv
}

func (f GrpcCommandService) TestConn(ctx context.Context, message *pbgocmd.SeqMessage) (*pbgocmd.AckMessage, error) {
	ack := &pbgocmd.AckMessage{
		Ack:     message.Seq + 1,
		MsgTime: time.Now().Unix(),
	}
	return ack, nil
}

func (f GrpcCommandService) ListCommand(ctx context.Context, request *pbgocmd.ListCmdRequest) (*pbgocmd.ListCmdResponse, error) {
	servicesList, err := f.executor.ListServiceName()
	if err != nil {
		return nil, err
	}

	services := make([]*pbgocmd.Service, len(servicesList))

	for i, info := range servicesList {
		s := &pbgocmd.Service{
			SrvId:     info.ServiceId,
			Name:      info.ServiceName,
			Introduce: "",
			Version:   info.Version,
			NodeInfo:  "",
			Cmds:      make([]*pbgocmd.Command, len(info.Funcs)),
		}
		for i2, funcInfo := range info.Funcs {
			cmdinfo := &pbgocmd.Command{
				FunctionID: funcInfo.FuncId,
				Name:       funcInfo.FuncName,
				Introduce:  "",
				ArgsInfo:   "",
				ReturnInfo: "",
			}
			s.Cmds[i2] = cmdinfo
		}
		services[i] = s
	}
	resp := &pbgocmd.ListCmdResponse{
		CmdHead:  nil,
		Services: services,
	}
	return resp, nil
}

func (f *GrpcCommandService) Command(ctx context.Context, request *pbgocmd.CommandRequest) (*pbgocmd.CommandResponse, error) {
	var reqCtx context.Context = ctx
	// 全局设置的命令调用超时时间
	if f.opts.cmdTimeout > 0 {
		reqCtx, _ = context.WithTimeout(ctx, f.opts.cmdTimeout)
	}
	//
	reqParams := command.SimpleFileCommand(
		command.SimpleCommand(
			request.GetServiceId(),
			request.GetFunctionId(),
			request.GetParam(),
		),
		command.NewByteAccessory(request.GetInSecureInfo(), request.GetInSecureData()),
	)
	//
	if head := request.GetCmdHead(); head != nil {
		if head.CallTimeout > 0 {
			reqCtx, _ = context.WithTimeout(reqCtx, xxtime.ParseMillSecond(int64(head.CallTimeout)))
		}
		if head.GetMsgTime() > 0 && head.GetMsgLiveTime() > 0 {
			// todo, 判断当前消息是否已过期, 并设置消息有效期时长
		}
		if ext := head.GetExtend(); ext != nil {
			h := reqParams.Head()
			for k, v := range ext {
				h[k] = v
			}
		}
	}
	//

	result := f.executor.ExecuteWithFile(reqCtx, reqParams)
	commandResult := result.GetCommandResult()
	head := &pbgocmd.MessageHead{
		Id:          request.CmdHead.GetId(),
		MsgTypes:    0,
		MsgTime:     time.Now().Unix(),
		MsgLiveTime: 0,
		CallTimeout: 0,
		Sign:        "",
		Extend:      commandResult.Head(),
	}
	cmdResp := &pbgocmd.CommandResponse{
		CmdHead: head,
		Code:    int32(commandResult.Code),
		Message: commandResult.ErrMsg(),
		Data:    commandResult.Result.Bytes(),
	}
	if accessory := result.GetAccessory(); accessory != nil && !accessory.IsEmpty() {
		if bs, err := ioutil.ReadAll(accessory.Reader()); err != nil {
			// todo
		} else {
			cmdResp.InSecureData = bs
			cmdResp.InSecureInfo = accessory.Info()
		}
	}
	return cmdResp, nil
}

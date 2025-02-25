package plugins

import (
	"fmt"

	"encoding/json"
	"errors"
	"strconv"
	"time"
        "strings"
	"github.com/WeBankPartners/wecube-plugins-qcloud/plugins/utils"
	"github.com/sirupsen/logrus"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
)

const (
	INSTANCE_STATE_RUNNING = "RUNNING"
)

const (
	QCLOUD_ENDPOINT_CVM              = "cvm.tencentcloudapi.com"
	INSTANCE_CHARGE_TYPE_PREPAID     = "PREPAID"
	RENEW_FLAG_NOTIFY_AND_AUTO_RENEW = "NOTIFY_AND_AUTO_RENEW"
)

var (
	INVALID_PARAMETERS          = errors.New("Invalid parameters")
	VM_WAIT_STATE_TIMEOUT_ERROR = errors.New("qcloud wait vm timeout")
	VM_NOT_FOUND_ERROR          = errors.New("qcloud vm not found")
)

type VmInputs struct {
	Inputs []VmInput `json:"inputs,omitempty"`
}

type VmInput struct {
	Guid                 string `json:"guid,omitempty"`
	Seed                 string `json:"seed,omitempty"`
	ProviderParams       string `json:"provider_params,omitempty"`
	VpcId                string `json:"vpc_id,omitempty"`
	SubnetId             string `json:"subnet_id,omitempty"`
	InstanceName         string `json:"instance_name,omitempty"`
	Id                   string `json:"id,omitempty"`
	InstanceType         string `json:"instance_type,omitempty"`
	ImageId              string `json:"image_id,omitempty"`
	SystemDiskSize       int64  `json:"system_disk_size,omitempty"`
	InstanceChargeType   string `json:"instance_charge_type,omitempty"`
	InstanceChargePeriod int64  `json:"instance_charge_period,omitempty"`
	InstancePrivateIp    string `json:"instance_private_ip,omitempty"`
	Password             string `json:"password,omitempty"`
	ProjectId            int64  `json:"project_id,omitempty"`
}

type VmOutputs struct {
	Outputs []VmOutput `json:"outputs,omitempty"`
}

type VmOutput struct {
	Guid              string `json:"guid,omitempty"`
	RequestId         string `json:"request_id,omitempty"`
	Id                string `json:"id,omitempty"`
	Cpu               string `json:"cpu,omitempty"`
	Memory            string `json:"memory,omitempty"`
	Password          string `json:"password,omitempty"`
	InstanceState     string `json:"instance_state,omitempty"`
	InstancePrivateIp string `json:"instance_private_ip,omitempty"`
}

type VmPlugin struct{}

var VMActions = make(map[string]Action)

func init() {
	VMActions["create"] = new(VMCreateAction)
	VMActions["terminate"] = new(VMTerminateAction)
	VMActions["start"] = new(VMStartAction)
	VMActions["stop"] = new(VMStopAction)
	VMActions["bind-security-groups"] = new(VMBindSecurityGroupsAction)
}

func (plugin *VmPlugin) GetActionByName(actionName string) (Action, error) {
	action, found := VMActions[actionName]
	if !found {
		return nil, fmt.Errorf("vmplugin,action[%s] not found", actionName)
	}
	return action, nil
}

type VMAction struct {
}

func (action *VMAction) ReadParam(param interface{}) (interface{}, error) {
	var inputs VmInputs
	err := UnmarshalJson(param, &inputs)
	if err != nil {
		return nil, err
	}
	return inputs, nil
}

func (action *VMAction) CheckParam(input interface{}) error {
	vms, ok := input.(VmInputs)
	if !ok {
		return INVALID_PARAMETERS
	}

	for _, vm := range vms.Inputs {
		if vm.Id == "" {
			return errors.New("input instance_id is empty")
		}
	}

	return nil
}

type QcloudRunInstanceStruct struct {
	Placement             PlacementStruct
	ImageId               string
	InstanceChargeType    string
	InstanceChargePrepaid *InstanceChargePrepaidStruct `json:"InstanceChargePrepaid,omitempty"`
	InstanceType          string
	SystemDisk            SystemDiskStruct          `json:"SystemDisk,omitempty"`
	DataDisks             []DataDisksStruct         `json:"DataDisks,omitempty"`
	VirtualPrivateCloud   VirtualPrivateCloudStruct `json:"VirtualPrivateCloud,omitempty"`
	LoginSettings         LoginSettingsStruct       `json:"LoginSettings,omitempty"`
	SecurityGroupIds      []string
	InternetAccessible    InternetAccessible
}

type InternetAccessible struct {
	PublicIpAssigned        bool `json:"PublicIpAssigned"`
	InternetMaxBandwidthOut int  `json:"InternetMaxBandwidthOut"`
}

type PlacementStruct struct {
	Zone      string
	ProjectId int64 `json:"ProjectId,omitempty"`
}

type InstanceChargePrepaidStruct struct {
	Period    int64  `json:"Period,omitempty"`
	RenewFlag string `json:"RenewFlag,omitempty"`
}
type SystemDiskStruct struct {
	DiskType string
	DiskSize int64
}
type DataDisksStruct struct {
	DiskSize           int64
	DiskType           string
	DeleteWithInstance bool
}
type VirtualPrivateCloudStruct struct {
	VpcId              string
	SubnetId           string
	PrivateIpAddresses []string `json:"PrivateIpAddresses,omitempty"`
}
type LoginSettingsStruct struct {
	Password string
}

func createCvmClient(region, secretId, secretKey string) (client *cvm.Client, err error) {
	credential := common.NewCredential(secretId, secretKey)

	clientProfile := profile.NewClientProfile()
	clientProfile.HttpProfile.Endpoint = QCLOUD_ENDPOINT_CVM

	client, err = cvm.NewClient(credential, region, clientProfile)
	if err != nil {
		logrus.Errorf("Create Qcloud vm client failed,err=%v", err)
	}
	return
}

func describeInstancesFromCvm(client *cvm.Client, describeInstancesParams cvm.DescribeInstancesRequest) (response *cvm.DescribeInstancesResponse, err error) {
	request := cvm.NewDescribeInstancesRequest()
	describeInstancesParamsByteArray, _ := json.Marshal(describeInstancesParams)
	request.FromJsonString(string(describeInstancesParamsByteArray))

	logrus.Debugf("Submit DescribeInstances request: %#v", string(describeInstancesParamsByteArray))
	response, err = client.DescribeInstances(request)
	logrus.Debugf("Submit DescribeInstances return: %v", response)

	if err != nil {
		logrus.Errorf("describeInstancesFromCvm meet error=%v", err)
	}
	return response, err
}

func getInstanceByInstanceId(client *cvm.Client, instanceId string) (*cvm.Instance, error) {
	describeInstancesParams := cvm.DescribeInstancesRequest{
		InstanceIds: []*string{&instanceId},
	}
	describeInstancesResponse, err := describeInstancesFromCvm(client, describeInstancesParams)
	if err != nil {
		return nil, err
	}

	if len(describeInstancesResponse.Response.InstanceSet) != 1 {
		logrus.Errorf("found vm[%s] have %d instance", instanceId, len(describeInstancesResponse.Response.InstanceSet))
		return nil, VM_NOT_FOUND_ERROR
	}

	return describeInstancesResponse.Response.InstanceSet[0], nil
}

func isInstanceInDesireState(client *cvm.Client, instanceId string, desireState string) error {
	instance, err := getInstanceByInstanceId(client, instanceId)
	if err != nil {
		return err
	}

	if *instance.InstanceState != desireState {
		return fmt.Errorf("qcloud instance not in desire state[%s],real state=%v", desireState, *instance.InstanceState)
	}

	return nil
}

func waitVmInDesireState(client *cvm.Client, instanceId string, desireState string, timeout int) error {
	count := 0

	for {
		time.Sleep(5 * time.Second)
		instance, err := getInstanceByInstanceId(client, instanceId)
		if err != nil {
			return err
		}

		if *instance.InstanceState == desireState {
			break
		}

		count++
		if count*5 > timeout {
			return VM_WAIT_STATE_TIMEOUT_ERROR
		}
	}
	return nil
}

func waitVmTerminateDone(client *cvm.Client, instanceId string, timeout int) error {
	count := 0
	describeInstancesParams := cvm.DescribeInstancesRequest{
		InstanceIds: []*string{&instanceId},
	}
	for {
		time.Sleep(5 * time.Second)
		describeInstancesResponse, err := describeInstancesFromCvm(client, describeInstancesParams)
		if err != nil {
			return err
		}
		if len(describeInstancesResponse.Response.InstanceSet) == 0 {
			break
		}

		count++
		if count*5 > timeout {
			return VM_WAIT_STATE_TIMEOUT_ERROR
		}
	}
	return nil
}

type VMCreateAction struct {
	VMAction
}

func (action *VMCreateAction) CheckParam(input interface{}) error {
	_, ok := input.(VmInputs)
	if !ok {
		return INVALID_PARAMETERS
	}

	return nil
}

func (action *VMCreateAction) Do(input interface{}) (interface{}, error) {
	vms, _ := input.(VmInputs)
	outputs := VmOutputs{}
	for _, vm := range vms.Inputs {
		output := VmOutput{}

		paramsMap, err := GetMapFromProviderParams(vm.ProviderParams)
		logrus.Debugf("actionParam:%v", vm)
		client, err := createCvmClient(paramsMap["Region"], paramsMap["SecretID"], paramsMap["SecretKey"])
		if err != nil {
			return nil, err
		}
		if vm.Password == ""{
			vm.Password = utils.CreateRandomPassword()
		}
		
		runInstanceRequest := QcloudRunInstanceStruct{
			Placement: PlacementStruct{
				Zone: paramsMap["AvailableZone"],
			},
			ImageId:            vm.ImageId,
			InstanceChargeType: vm.InstanceChargeType,
			InstanceType:       vm.InstanceType,
			SystemDisk: SystemDiskStruct{
				DiskType: "CLOUD_PREMIUM",
				DiskSize: vm.SystemDiskSize,
			},
			VirtualPrivateCloud: VirtualPrivateCloudStruct{
				VpcId:    vm.VpcId,
				SubnetId: vm.SubnetId,
			},
			LoginSettings: LoginSettingsStruct{
				Password: vm.Password,
			},
			InternetAccessible: InternetAccessible{
				PublicIpAssigned:        false,
				InternetMaxBandwidthOut: 10,
			},
		}
		if vm.ProjectId != 0 {
			runInstanceRequest.Placement.ProjectId=vm.ProjectId
		}

		if vm.InstancePrivateIp != "" {
			runInstanceRequest.VirtualPrivateCloud.PrivateIpAddresses = []string{vm.InstancePrivateIp}
		}

		if vm.InstanceChargeType == INSTANCE_CHARGE_TYPE_PREPAID {
			runInstanceRequest.InstanceChargePrepaid = &InstanceChargePrepaidStruct{
				Period:    vm.InstanceChargePeriod,
				RenewFlag: RENEW_FLAG_NOTIFY_AND_AUTO_RENEW,
			}
		}

		//check resources exsit
		if vm.Id != "" {
			describeInstancesParams := cvm.DescribeInstancesRequest{
				InstanceIds: []*string{&vm.Id},
			}

			describeInstancesResponse, err := describeInstancesFromCvm(client, describeInstancesParams)
			if err != nil {
				return nil, err
			}

			if len(describeInstancesResponse.Response.InstanceSet) > 1 {
				logrus.Errorf("check vm exsit found vm[%s] have %d instance", vm.Id, len(describeInstancesResponse.Response.InstanceSet))
				return nil, VM_NOT_FOUND_ERROR
			}

			if len(describeInstancesResponse.Response.InstanceSet) == 1 {
				output.RequestId = *describeInstancesResponse.Response.RequestId
				output.Guid = vm.Guid
				output.Id = vm.Id
				output.Memory = strconv.Itoa(int(*describeInstancesResponse.Response.InstanceSet[0].Memory))
				output.Cpu = strconv.Itoa(int(*describeInstancesResponse.Response.InstanceSet[0].CPU))
				output.InstanceState = *describeInstancesResponse.Response.InstanceSet[0].InstanceState
				output.InstancePrivateIp = *describeInstancesResponse.Response.InstanceSet[0].PrivateIpAddresses[0]
				outputs.Outputs = append(outputs.Outputs, output)
				continue
			}
		}

		//check resources exsit
		if vm.Id != "" {
			describeInstancesParams := cvm.DescribeInstancesRequest{
				InstanceIds: []*string{&vm.Id},
			}

			describeInstancesResponse, err := describeInstancesFromCvm(client, describeInstancesParams)
			if err != nil {
				return nil, err
			}

			if len(describeInstancesResponse.Response.InstanceSet) > 1 {
				logrus.Errorf("check vm exsit found vm[%s] have %d instance", vm.Id, len(describeInstancesResponse.Response.InstanceSet))
				return nil, VM_NOT_FOUND_ERROR
			}

			if len(describeInstancesResponse.Response.InstanceSet) == 1 {
				output.RequestId = *describeInstancesResponse.Response.RequestId
				output.Guid = vm.Guid
				output.Id = vm.Id
				output.Memory = strconv.Itoa(int(*describeInstancesResponse.Response.InstanceSet[0].Memory))
				output.Cpu = strconv.Itoa(int(*describeInstancesResponse.Response.InstanceSet[0].CPU))
				output.InstanceState = *describeInstancesResponse.Response.InstanceSet[0].InstanceState
				output.InstancePrivateIp = *describeInstancesResponse.Response.InstanceSet[0].PrivateIpAddresses[0]
				outputs.Outputs = append(outputs.Outputs, output)

				continue
			}
		}

		request := cvm.NewRunInstancesRequest()
		byteRunInstancesRequestData, _ := json.Marshal(runInstanceRequest)
		logrus.Debugf("byteRunInstancesRequestData=%v", string(byteRunInstancesRequestData))
		request.FromJsonString(string(byteRunInstancesRequestData))
		if vm.InstanceName!=""{
			request.InstanceName=&vm.InstanceName
		}

		resp, err := client.RunInstances(request)
		if err != nil {
			return nil, err
		}

		vm.Id = *resp.Response.InstanceIdSet[0]
		logrus.Infof("Create VM's request has been submitted, InstanceId is [%v], RequestID is [%v]", vm.Id, *resp.Response.RequestId)

		if err = waitVmInDesireState(client, vm.Id, INSTANCE_STATE_RUNNING, 120); err != nil {
			return nil, err
		}
		logrus.Infof("Created VM's state is [%v] now", INSTANCE_STATE_RUNNING)

		describeInstancesParams := cvm.DescribeInstancesRequest{
			InstanceIds: []*string{&vm.Id},
		}

		describeInstancesResponse, err := describeInstancesFromCvm(client, describeInstancesParams)
		if err != nil {
			return nil, err
		}

		md5sum := utils.Md5Encode(vm.Guid + vm.Seed)
		if output.Password, err = utils.AesEncode(md5sum[0:16], vm.Password); err != nil {
			logrus.Errorf("AesEncode meet error(%v)", err)
			return nil, errors.New("aes encode error")
		}

		output.RequestId = *describeInstancesResponse.Response.RequestId
		output.Guid = vm.Guid
		output.Id = vm.Id
		output.Memory = strconv.Itoa(int(*describeInstancesResponse.Response.InstanceSet[0].Memory))
		output.Cpu = strconv.Itoa(int(*describeInstancesResponse.Response.InstanceSet[0].CPU))
		output.InstanceState = *describeInstancesResponse.Response.InstanceSet[0].InstanceState
		output.InstancePrivateIp = *describeInstancesResponse.Response.InstanceSet[0].PrivateIpAddresses[0]
		outputs.Outputs = append(outputs.Outputs, output)
	}

	return &outputs, nil
}

type VMTerminateAction struct {
	VMAction
}

func (action *VMTerminateAction) Do(input interface{}) (interface{}, error) {
	vms, _ := input.(VmInputs)
	outputs := VmOutputs{}

	for _, vm := range vms.Inputs {
		paramsMap, err := GetMapFromProviderParams(vm.ProviderParams)

		terminateInstancesRequestData := cvm.TerminateInstancesRequest{
			InstanceIds: []*string{&vm.Id},
		}

		client, err := createCvmClient(paramsMap["Region"], paramsMap["SecretID"], paramsMap["SecretKey"])
		if err != nil {
			return nil, err
		}
		terminateInstancesRequest := cvm.NewTerminateInstancesRequest()
		byteTerminateInstancesRequestData, _ := json.Marshal(terminateInstancesRequestData)
		terminateInstancesRequest.FromJsonString(string(byteTerminateInstancesRequestData))

		response, err := client.TerminateInstances(terminateInstancesRequest)
		if err != nil {
			return nil, err
		}
		logrus.Infof("Terminate VM[%v] has been submitted in Qcloud, RequestID is [%v]", vm.Id, *response.Response.RequestId)

		if err = waitVmTerminateDone(client, vm.Id, 600); err != nil {
			return nil, err
		}
		output := VmOutput{}
		output.RequestId = *response.Response.RequestId
		output.Guid = vm.Guid
		output.Id = vm.Id

		outputs.Outputs = append(outputs.Outputs, output)

		logrus.Infof("Terminated VM[%v] has been done", vm.Id)
	}

	return &outputs, nil
}

type VMStartAction struct {
	VMAction
}

func (action *VMStartAction) Do(input interface{}) (interface{}, error) {
	vms, _ := input.(VmInputs)
	outputs := VmOutputs{}
	for _, vm := range vms.Inputs {
		requestId, err := action.startInstance(vm)
		if err != nil {
			return nil, err
		}

		output := VmOutput{}
		output.RequestId = requestId
		output.Guid = vm.Guid
		output.Id = vm.Id
		outputs.Outputs = append(outputs.Outputs, output)
	}

	return &outputs, nil
}

func (action *VMStartAction) startInstance(vm VmInput) (string, error) {
	paramsMap, _ := GetMapFromProviderParams(vm.ProviderParams)

	client, err := createCvmClient(paramsMap["Region"], paramsMap["SecretID"], paramsMap["SecretKey"])
	if err != nil {
		return "", err
	}

	request := cvm.NewStartInstancesRequest()
	request.InstanceIds = append(request.InstanceIds, &vm.Id)

	response, err := client.StartInstances(request)
	if err != nil {
		return "", err
	}
	return *response.Response.RequestId, nil
}

type VMStopAction struct {
	VMAction
}

func (action *VMStopAction) Do(input interface{}) (interface{}, error) {
	vms, _ := input.(VmInputs)
	outputs := VmOutputs{}
	for _, vm := range vms.Inputs {
		output, err := action.stopInstance(&vm)
		if err != nil {
			return nil, err
		}

		outputs.Outputs = append(outputs.Outputs, *output)
	}

	return &outputs, nil
}

func (action *VMStopAction) stopInstance(vm *VmInput) (*VmOutput, error) {
	paramsMap, _ := GetMapFromProviderParams(vm.ProviderParams)

	client, err := createCvmClient(paramsMap["Region"], paramsMap["SecretID"], paramsMap["SecretKey"])
	if err != nil {
		return nil, err
	}

	request := cvm.NewStopInstancesRequest()
	request.InstanceIds = append(request.InstanceIds, &vm.Id)

	response, err := client.StopInstances(request)
	if err != nil {
		return nil, err
	}

	output := VmOutput{}
	output.RequestId = *response.Response.RequestId
	output.Guid = vm.Guid
	output.Id = vm.Id

	return &output, nil
}

func QueryCvmInstance(providerParams string, filter Filter) ([]*cvm.Instance, error) {
	validFilterNames := []string{"instanceId", "privateIpAddress"}
	filterValues := common.StringPtrs(filter.Values)
	var limit int64

	paramsMap, err := GetMapFromProviderParams(providerParams)
	if err != nil {
		return nil, err
	}
	client, err := createCvmClient(paramsMap["Region"], paramsMap["SecretID"], paramsMap["SecretKey"])
	if err != nil {
		return nil, err
	}

	if err := IsValidValue(filter.Name, validFilterNames); err != nil {
		return nil, err
	}

	request := cvm.NewDescribeInstancesRequest()
	limit = int64(len(filterValues))
	request.Limit = &limit
	name, err := TransLittleCamelcaseToShortLineFormat(filter.Name)
	if err != nil {
		return nil, err
	}
	
	cvmFilter := &cvm.Filter{
		Name:   common.StringPtr(name),
		Values: common.StringPtrs(filter.Values),
	}
	request.Filters = append(request.Filters, cvmFilter)

	response, err := client.DescribeInstances(request)
	if err != nil {
		logrus.Errorf("cvm DescribeInstances meet err=%v", err)
		return nil, err
	}

	return response.Response.InstanceSet, nil
}

func BindCvmInstanceSecurityGroups(providerParams string, instanceId string, securityGroups []string) error {
	paramsMap, err := GetMapFromProviderParams(providerParams)
	if err != nil {
		return err
	}
	client, err := createCvmClient(paramsMap["Region"], paramsMap["SecretID"], paramsMap["SecretKey"])
	if err != nil {
		return err
	}

	request := cvm.NewModifyInstancesAttributeRequest()
	request.InstanceIds = common.StringPtrs([]string{instanceId})
	request.SecurityGroups = common.StringPtrs(securityGroups)
	if _, err = client.ModifyInstancesAttribute(request); err != nil {
		logrus.Errorf("cvm AssociateSecurityGroups meet err=%v", err)
	}

	return err
}

//--------------bind security group to vm--------------------//
type VMBindSecurityGroupsAction struct {

}

type VmBindSecurityGroupInputs struct {
	Inputs []VmBindSecurityGroupInput `json:"inputs,omitempty"`
}

type VmBindSecurityGroupInput struct {
	Guid                 string `json:"guid,omitempty"`
	ProviderParams       string `json:"provider_params,omitempty"`
	InstanceId           string `json:"instance_id,omitempty"`
	SecurityGroupIds     string `json:"security_group_ids,omitempty"`
}

type VmBindSecurityGroupOutputs struct {
	Outputs []VmBindSecurityGroupOutput `json:"outputs,omitempty"`
}

type VmBindSecurityGroupOutput struct {
	Guid                 string `json:"guid,omitempty"`
}

func (action *VMBindSecurityGroupsAction) ReadParam(param interface{}) (interface{}, error) {
	var inputs VmBindSecurityGroupInputs
	err := UnmarshalJson(param, &inputs)
	if err != nil {
		return nil, err
	}
	return inputs, nil
}

func (action *VMBindSecurityGroupsAction) CheckParam(input interface{}) error {
	inputs, ok := input.(VmBindSecurityGroupInputs)
	if !ok {
		return fmt.Errorf("VMBindSecurityGroupsAction:input type=%T not right", input)
	}

	for _,input:=range inputs.Inputs{
		if input.ProviderParams == ""{
			return errors.New("providerParams is empty")
		}

		if input.InstanceId == "" {
			return errors.New("instanceId is empty")
		}

		if input.SecurityGroupIds == "" {
			return errors.New("securityGroupIds is empty")
		}
	}

	return nil
}

func (action *VMBindSecurityGroupsAction)Do(input interface{}) (interface{}, error) {
	inputs, _ := input.(VmBindSecurityGroupInputs)
	outputs:=VmBindSecurityGroupOutputs{}

	for _,input:=range inputs.Inputs{
		securityGroups:=strings.Split(input.SecurityGroupIds,",")
		err:=BindCvmInstanceSecurityGroups(input.ProviderParams,input.InstanceId,securityGroups)
		if err != nil {
			return nil,err
		}
		output:=VmBindSecurityGroupOutput{
			Guid:input.Guid,
		}
		outputs.Outputs=append(outputs.Outputs,output)
	}

	return outputs,nil 
}

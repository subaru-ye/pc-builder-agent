package schemas

// ResolvedBuild 规则引擎的唯一输入:store 层按 SKU 从 PostgreSQL 展开的规格真值。
// 除 GPU 外其余七类必选部件在构造时必须齐备(缺失属于 schema error,
// 由 store 层拒绝);零件内部字段缺失(nil)才由规则层产出 unknown。
type ResolvedBuild struct {
	BuildRef    string
	CPU         CPUSpec
	GPU         *GPUSpec // nil = 显式无独显(BuildSelection 中 gpu:null),不是未知
	Motherboard MotherboardSpec
	Memory      MemorySpec
	SSDs        []ResolvedSSD
	PSU         PSUSpec
	Case        CaseSpec
	Cooler      CoolerSpec
}

// ResolvedSSD 一条展开后的 SSD:规格 + 数量。
type ResolvedSSD struct {
	Spec     SSDSpec
	Quantity int
}

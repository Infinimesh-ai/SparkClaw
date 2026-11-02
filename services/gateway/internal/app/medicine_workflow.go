package app

const (
	CapabilityMedicineBoxQuery  CapabilityID   = "medicine.box_query"
	CapabilityMedicineBoxEdit   CapabilityID   = "medicine.box_edit"
	CapabilityMedicineDrugInfo  CapabilityID   = "medicine.drug_info"
	WorkflowMedicineBoxQuery    WorkflowID     = "medicine.box_query"
	WorkflowMedicineBoxEdit     WorkflowID     = "medicine.box_edit"
	WorkflowMedicineDrugInfo    WorkflowID     = "medicine.drug_info"
	RouteFactScopeMedicineBox   RouteFactScope = "medicine_box"
	IntentDomainMedicine        IntentDomain   = "medicine"
	ToolCapabilityMedicineRead                 = "medicine.box.read"
	ToolCapabilityMedicineWrite                = "medicine.box.write"
	ToolCapabilityMedicineInfo                 = "medicine.info.read"
	ToolCapabilityWorkflowInput                = "workflow.input"
)

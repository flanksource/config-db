package aws

// func getPatchStates(ctx *AWSContext, config v1.AWS, instances []string) {
// 	if awsConfig.PatchStates {
// 		return
// 	}

// if awsConfig.Inventory {
// 	inventory, err := awsCtx.SSM.GetInventory(ctx, nil)
// 	if err != nil {
// 		return results.Errorf(err, "failed to get inventory")
// 	}

// 	for _, inv := range inventory.Entities {
// 		if data, ok := inv.Data["AWS:InstanceInformation"]; ok {
// 			instance, found := instances[*inv.Id]
// 			if !found {
// 				logger.Warnf("Inventory found for unknown instance %s", *inv.Id)
// 				continue
// 			}
// 			instance.Inventory = makeMap(instance.Inventory)
// 			for _, d := range data.Content {
// 				for k, v := range d {
// 					instance.Inventory[k] = v
// 				}
// 			}
// 			instance.Inventory["CaptureTime"] = *data.CaptureTime
// 		}
// 	}
// }
// patchStates, err := ctx.SSM.DescribeInstancePatchStates(ctx, &ssm.DescribeInstancePatchStatesInput{
// 	InstanceIds: getKeys(instances),
// })
// if err != nil {
// 	return results.Errorf(err, "failed to retrieve patch states")
// }
// for _, patch := range patchStates.InstancePatchStates {
// 	instance, found := instances[*patch.InstanceId]
// 	if !found {
// 		logger.Warnf("Patch found for unknown instance %s", *patch.InstanceId)
// 		continue
// 	}
// 	if instance.PatchState != nil {
// 		logger.Warnf("Duplicate patch found for %s, %v and %v", instance.InstanceID, instance.PatchState.OperationEndTime, patch.OperationEndTime)
// 	}
// 	instance.PatchState = &patch
// }
// }

// func listPatches(ctx *AWSContext, instanceID string, token *string) ([]PatchDetail, error) {
// 	var list = []PatchDetail{}

// 	patches, err := ctx.SSM.DescribeInstancePatches(ctx, &ssm.DescribeInstancePatchesInput{
// 		InstanceId: &instanceID,
// 		MaxResults: 50,
// 		NextToken:  token,
// 	})

// 	if err != nil {
// 		return nil, errors.Wrapf(err, "failed to get patches for %s", instanceID)
// 	}

// 	for _, p := range patches.Patches {
// 		if p.State != "NotApplicable" {
// 			list = append(list, NewPatchDetail(p))
// 		}
// 	}
// 	if patches.NextToken != nil {
// 		nextList, err := listPatches(ctx.SSM, ctx, instanceID, patches.NextToken)
// 		if err != nil {
// 			return nil, errors.Wrapf(err, "failed to get patches for %s", instanceID)
// 		}
// 		list = append(list, nextList...)
// 	}
// 	return list, nil
// }

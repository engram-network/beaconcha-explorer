package handlers

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

// Index will return the main "index" page using a go template
func Index(w http.ResponseWriter, r *http.Request) {
	var indexTemplateFiles = append(layoutTemplateFiles,
		"index/index.html",
		"index/networkOverview.html",
		"index/recentBlocks.html",
		"index/recentEpochs.html",
		"index/recentSlots.html",
		"_svg/professor.html",
		"_svg/timeline.html",
	)

	var indexTemplate = templates.GetTemplate(indexTemplateFiles...)

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "index", "", "", indexTemplateFiles)
	data.Data = getIndexPageData()

	if handleTemplateError(w, r, "index.go", "Index", "", indexTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getIndexPageData() *models.IndexPageData {
	pageData := &models.IndexPageData{}
	pageCacheKey := "index"
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildIndexPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.IndexPageData)
	return pageData
}

func buildIndexPageData() (*models.IndexPageData, time.Duration) {
	logrus.Printf("index page called")

	recentEpochCount := 7
	recentBlockCount := 7
	recentSlotsCount := 16

	// network overview
	now := time.Now()
	currentEpoch := utils.TimeToEpoch(now)
	if currentEpoch < 0 {
		currentEpoch = 0
	}
	currentSlot := utils.TimeToSlot(uint64(now.Unix()))
	currentSlotIndex := (currentSlot % utils.Config.Chain.Config.SlotsPerEpoch) + 1

	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()

	syncState := dbtypes.IndexerSyncState{}
	db.GetExplorerState("indexer.syncstate", &syncState)
	var isSynced bool
	if finalizedEpoch >= 1 {
		isSynced = syncState.Epoch >= uint64(finalizedEpoch-1)
	} else {
		isSynced = true
	}

	pageData := &models.IndexPageData{
		NetworkName:           utils.Config.Chain.Name,
		DepositContract:       utils.Config.Chain.Config.DepositContractAddress,
		ShowSyncingMessage:    !isSynced,
		CurrentEpoch:          uint64(currentEpoch),
		CurrentFinalizedEpoch: finalizedEpoch,
		CurrentSlot:           currentSlot,
		CurrentSlotIndex:      currentSlotIndex,
		CurrentScheduledCount: utils.Config.Chain.Config.SlotsPerEpoch - currentSlotIndex,
		CurrentEpochProgress:  float64(100) * float64(currentSlotIndex) / float64(utils.Config.Chain.Config.SlotsPerEpoch),
	}
	if utils.Config.Chain.DisplayName != "" {
		pageData.NetworkName = utils.Config.Chain.DisplayName
	}

	currentValidatorSet := services.GlobalBeaconService.GetCachedValidatorSet()
	if currentValidatorSet != nil {
		for _, validator := range currentValidatorSet.Data {
			if strings.HasPrefix(validator.Status, "active") {
				pageData.ActiveValidatorCount++
				pageData.TotalEligibleEther += uint64(validator.Validator.EffectiveBalance)
				pageData.AverageValidatorBalance += uint64(validator.Balance)
			}
			if validator.Status == "pending_queued" {
				pageData.EnteringValidatorCount++
			}
			if validator.Status == "active_exiting" {
				pageData.ExitingValidatorCount++
			}
		}
		if pageData.AverageValidatorBalance > 0 {
			pageData.AverageValidatorBalance = pageData.AverageValidatorBalance / pageData.ActiveValidatorCount
		}
	}
	pageData.ValidatorsPerEpoch = utils.GetValidatorChurnLimit(pageData.ActiveValidatorCount)
	pageData.ValidatorsPerDay = pageData.ValidatorsPerEpoch * 225
	depositQueueTime := float64(pageData.EnteringValidatorCount) / float64(pageData.ValidatorsPerDay)
	if depositQueueTime > 0 {
		depositQueueDays, depositQueueFractionalDays := math.Modf(depositQueueTime)
		depositQueueHours := int(depositQueueFractionalDays * 24)
		pageData.NewDepositProcessAfter = fmt.Sprintf("%d days and %d hours", int(depositQueueDays), depositQueueHours)
	}

	networkGenesis, _ := services.GlobalBeaconService.GetGenesis()
	if networkGenesis != nil {
		pageData.GenesisTime = time.Unix(int64(networkGenesis.Data.GenesisTime), 0)
		pageData.GenesisForkVersion = networkGenesis.Data.GenesisForkVersion
		pageData.GenesisValidatorsRoot = networkGenesis.Data.GenesisValidatorsRoot
	}

	pageData.NetworkForks = make([]*models.IndexPageDataForks, 0)
	if utils.Config.Chain.Config.AltairForkEpoch < uint64(18446744073709551615) && utils.Config.Chain.Config.AltairForkVersion != "" {
		pageData.NetworkForks = append(pageData.NetworkForks, &models.IndexPageDataForks{
			Name:    "Altair",
			Epoch:   utils.Config.Chain.Config.AltairForkEpoch,
			Version: utils.MustParseHex(utils.Config.Chain.Config.AltairForkVersion),
			Active:  uint64(currentEpoch) >= utils.Config.Chain.Config.AltairForkEpoch,
		})
	}
	if utils.Config.Chain.Config.BellatrixForkEpoch < uint64(18446744073709551615) && utils.Config.Chain.Config.BellatrixForkVersion != "" {
		pageData.NetworkForks = append(pageData.NetworkForks, &models.IndexPageDataForks{
			Name:    "Bellatrix",
			Epoch:   utils.Config.Chain.Config.BellatrixForkEpoch,
			Version: utils.MustParseHex(utils.Config.Chain.Config.BellatrixForkVersion),
			Active:  uint64(currentEpoch) >= utils.Config.Chain.Config.BellatrixForkEpoch,
		})
	}
	if utils.Config.Chain.Config.CappellaForkEpoch < uint64(18446744073709551615) && utils.Config.Chain.Config.CappellaForkVersion != "" {
		pageData.NetworkForks = append(pageData.NetworkForks, &models.IndexPageDataForks{
			Name:    "Cappella",
			Epoch:   utils.Config.Chain.Config.CappellaForkEpoch,
			Version: utils.MustParseHex(utils.Config.Chain.Config.CappellaForkVersion),
			Active:  uint64(currentEpoch) >= utils.Config.Chain.Config.CappellaForkEpoch,
		})
	}
	if utils.Config.Chain.Config.DenebForkEpoch < uint64(18446744073709551615) && utils.Config.Chain.Config.DenebForkVersion != "" {
		pageData.NetworkForks = append(pageData.NetworkForks, &models.IndexPageDataForks{
			Name:    "Deneb",
			Epoch:   utils.Config.Chain.Config.DenebForkEpoch,
			Version: utils.MustParseHex(utils.Config.Chain.Config.DenebForkVersion),
			Active:  uint64(currentEpoch) >= utils.Config.Chain.Config.DenebForkEpoch,
		})
	}

	// load recent epochs
	buildIndexPageRecentEpochsData(pageData, uint64(currentEpoch), finalizedEpoch, recentEpochCount)

	// load recent blocks
	buildIndexPageRecentBlocksData(pageData, currentSlot, recentBlockCount)

	// load recent slots
	buildIndexPageRecentSlotsData(pageData, currentSlot, recentSlotsCount)

	return pageData, 12 * time.Second
}

func buildIndexPageRecentEpochsData(pageData *models.IndexPageData, currentEpoch uint64, finalizedEpoch int64, recentEpochCount int) {
	pageData.RecentEpochs = make([]*models.IndexPageDataEpochs, 0)
	epochsData := services.GlobalBeaconService.GetDbEpochs(currentEpoch, uint32(recentEpochCount))
	for i := 0; i < len(epochsData); i++ {
		epochData := epochsData[i]
		if epochData == nil {
			continue
		}
		voteParticipation := float64(1)
		if epochData.Eligible > 0 {
			voteParticipation = float64(epochData.VotedTarget) * 100.0 / float64(epochData.Eligible)
		}
		pageData.RecentEpochs = append(pageData.RecentEpochs, &models.IndexPageDataEpochs{
			Epoch:             epochData.Epoch,
			Ts:                utils.EpochToTime(epochData.Epoch),
			Finalized:         finalizedEpoch >= int64(epochData.Epoch),
			EligibleEther:     epochData.Eligible,
			TargetVoted:       epochData.VotedTarget,
			HeadVoted:         epochData.VotedHead,
			TotalVoted:        epochData.VotedTotal,
			VoteParticipation: voteParticipation,
		})
	}
	pageData.RecentEpochCount = uint64(len(pageData.RecentEpochs))
}

func buildIndexPageRecentBlocksData(pageData *models.IndexPageData, currentSlot uint64, recentBlockCount int) {
	pageData.RecentBlocks = make([]*models.IndexPageDataBlocks, 0)
	blocksData := services.GlobalBeaconService.GetDbBlocks(uint64(currentSlot), int32(recentBlockCount), false)
	for i := 0; i < len(blocksData); i++ {
		blockData := blocksData[i]
		if blockData == nil {
			continue
		}
		blockStatus := 1
		if blockData.Orphaned {
			blockStatus = 2
		}
		pageData.RecentBlocks = append(pageData.RecentBlocks, &models.IndexPageDataBlocks{
			Epoch:        utils.EpochOfSlot(blockData.Slot),
			Slot:         blockData.Slot,
			EthBlock:     blockData.EthBlockNumber,
			Ts:           utils.SlotToTime(blockData.Slot),
			Proposer:     blockData.Proposer,
			ProposerName: services.GlobalBeaconService.GetValidatorName(blockData.Proposer),
			Status:       uint64(blockStatus),
			BlockRoot:    blockData.Root,
		})
	}
	pageData.RecentBlockCount = uint64(len(pageData.RecentBlocks))
}

func buildIndexPageRecentSlotsData(pageData *models.IndexPageData, firstSlot uint64, slotLimit int) {
	var lastSlot uint64
	if firstSlot >= uint64(slotLimit) {
		lastSlot = firstSlot - uint64(slotLimit)
	} else {
		lastSlot = 0
	}

	// get slot assignments
	firstEpoch := utils.EpochOfSlot(firstSlot)
	lastEpoch := utils.EpochOfSlot(lastSlot)
	slotAssignments, _ := services.GlobalBeaconService.GetProposerAssignments(firstEpoch, lastEpoch)

	// load slots
	pageData.RecentSlots = make([]*models.IndexPageDataSlots, 0)
	dbSlots := services.GlobalBeaconService.GetDbBlocksForSlots(uint64(firstSlot), uint32(slotLimit), true)
	dbIdx := 0
	dbCnt := len(dbSlots)
	blockCount := uint64(0)
	openForks := map[int][]byte{}
	maxOpenFork := 0
	for slotIdx := int64(firstSlot); slotIdx >= int64(lastSlot); slotIdx-- {
		slot := uint64(slotIdx)
		haveBlock := false
		for dbIdx < dbCnt && dbSlots[dbIdx] != nil && dbSlots[dbIdx].Slot == slot {
			dbSlot := dbSlots[dbIdx]
			dbIdx++
			blockStatus := uint64(1)
			if dbSlot.Orphaned {
				blockStatus = 2
			}

			slotData := &models.IndexPageDataSlots{
				Slot:         slot,
				Epoch:        utils.EpochOfSlot(slot),
				Ts:           utils.SlotToTime(slot),
				Status:       blockStatus,
				Proposer:     dbSlot.Proposer,
				ProposerName: services.GlobalBeaconService.GetValidatorName(dbSlot.Proposer),
				BlockRoot:    dbSlot.Root,
				ParentRoot:   dbSlot.ParentRoot,
				ForkGraph:    make([]*models.IndexPageDataForkGraph, 0),
			}
			pageData.RecentSlots = append(pageData.RecentSlots, slotData)
			blockCount++
			haveBlock = true
			buildIndexPageSlotGraph(pageData, slotData, &maxOpenFork, openForks)
		}

		if !haveBlock {
			epoch := utils.EpochOfSlot(slot)
			slotData := &models.IndexPageDataSlots{
				Slot:         slot,
				Epoch:        epoch,
				Ts:           utils.SlotToTime(slot),
				Status:       0,
				Proposer:     slotAssignments[slot],
				ProposerName: services.GlobalBeaconService.GetValidatorName(slotAssignments[slot]),
				ForkGraph:    make([]*models.IndexPageDataForkGraph, 0),
			}
			pageData.RecentSlots = append(pageData.RecentSlots, slotData)
			blockCount++
			buildIndexPageSlotGraph(pageData, slotData, &maxOpenFork, openForks)
		}
	}
	pageData.RecentSlotCount = uint64(blockCount)
	pageData.ForkTreeWidth = (maxOpenFork * 20) + 20
}

func buildIndexPageSlotGraph(pageData *models.IndexPageData, slotData *models.IndexPageDataSlots, maxOpenFork *int, openForks map[int][]byte) {
	// fork tree
	var forkGraphIdx int = -1
	var freeForkIdx int = -1
	getForkGraph := func(slotData *models.IndexPageDataSlots, forkIdx int) *models.IndexPageDataForkGraph {
		forkGraph := &models.IndexPageDataForkGraph{}
		graphCount := len(slotData.ForkGraph)
		if graphCount > forkIdx {
			forkGraph = slotData.ForkGraph[forkIdx]
		} else {
			for graphCount <= forkIdx {
				forkGraph = &models.IndexPageDataForkGraph{
					Index: graphCount,
					Left:  10 + (graphCount * 20),
					Tiles: map[string]bool{},
				}
				slotData.ForkGraph = append(slotData.ForkGraph, forkGraph)
				graphCount++
			}
		}
		return forkGraph
	}

	for forkIdx := 0; forkIdx < *maxOpenFork; forkIdx++ {
		forkGraph := getForkGraph(slotData, forkIdx)
		if openForks[forkIdx] == nil {
			if freeForkIdx == -1 {
				freeForkIdx = forkIdx
			}
			continue
		} else {
			forkGraph.Tiles["vline"] = true
			if bytes.Equal(openForks[forkIdx], slotData.BlockRoot) {
				if forkGraphIdx != -1 {
					continue
				}
				forkGraphIdx = forkIdx
				openForks[forkIdx] = slotData.ParentRoot
				forkGraph.Block = true
				for targetIdx := forkIdx + 1; targetIdx < *maxOpenFork; targetIdx++ {
					if openForks[targetIdx] == nil || !bytes.Equal(openForks[targetIdx], slotData.BlockRoot) {
						continue
					}
					for idx := forkIdx + 1; idx <= targetIdx; idx++ {
						splitGraph := getForkGraph(slotData, idx)
						if idx == targetIdx {
							splitGraph.Tiles["tline"] = true
							splitGraph.Tiles["lline"] = true
							splitGraph.Tiles["fork"] = true
						} else {
							splitGraph.Tiles["hline"] = true
						}
					}
					forkGraph.Tiles["rline"] = true
					openForks[targetIdx] = nil
				}
			}
		}
	}
	if forkGraphIdx == -1 && slotData.Status > 0 {
		// fork head
		if freeForkIdx == -1 {
			freeForkIdx = *maxOpenFork
			*maxOpenFork++
		}
		openForks[freeForkIdx] = slotData.ParentRoot
		forkGraph := getForkGraph(slotData, freeForkIdx)
		forkGraph.Block = true
		forkGraph.Tiles["bline"] = true
	}
}
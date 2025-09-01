package Gogeo

/*
#include "osgeo_utils.h"
// 执行带进度监测的裁剪分析
static OGRErr performClipWithProgress(OGRLayerH inputLayer,
                                     OGRLayerH methodLayer,
                                     OGRLayerH resultLayer,
                                     char **options,
                                     void *progressData) {
    return OGR_L_Clip(inputLayer, methodLayer, resultLayer, options,
                      progressCallback, progressData);
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

// SpatialClipAnalysis并行空间裁剪分析
func SpatialClipAnalysis(layer1, layer2 *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error) {

	defer layer1.Close()

	defer layer2.Close()

	// 执行并行裁剪分析
	table1 := layer1.GetLayerName()
	table2 := layer2.GetLayerName()
	resultLayer, err := performParallelClipAnalysis(layer1, layer2, table1, table2, config)
	if err != nil {
		return nil, fmt.Errorf("执行并行裁剪分析失败: %v", err)
	}

	// 计算结果数量
	resultCount := resultLayer.GetFeatureCount()

	return &GeosAnalysisResult{
		OutputLayer: resultLayer,
		ResultCount: resultCount,
	}, nil
}

func performParallelClipAnalysis(layer1, layer2 *GDALLayer, table1Name, table2Name string, config *ParallelGeosConfig) (*GDALLayer, error) {
	// 获取数据范围 - 主要基于输入图层的范围
	extent, err := getInputLayerExtent(layer1)
	if err != nil {
		return nil, fmt.Errorf("获取输入图层范围失败: %v", err)
	}

	// 创建分块
	tiles := createTiles(extent, config.TileCount, config.BufferDistance)
	fmt.Printf("创建了 %d 个分块进行并行处理\n", len(tiles))

	// 创建结果图层 - 基于输入图层结构
	resultLayer, err := createClipResultLayer(layer1, layer2, table1Name, table2Name)
	if err != nil {
		return nil, fmt.Errorf("创建结果图层失败: %v", err)
	}

	// 为每个工作线程创建图层副本
	layer1Copies, layer2Copies, err := createLayerCopies(layer1, layer2, config.MaxWorkers)
	if err != nil {
		return nil, fmt.Errorf("创建图层副本失败: %v", err)
	}
	defer cleanupLayerCopies(layer1Copies, layer2Copies)

	// 并行处理分块
	err = processClipTilesInParallelSafe(layer1Copies, layer2Copies, resultLayer, tiles, table1Name, table2Name, config)
	if err != nil {
		return nil, fmt.Errorf("并行处理分块失败: %v", err)
	}

	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("并行裁剪分析完成，共生成 %d 个裁剪要素\n", resultCount)

	return resultLayer, nil
}

// getInputLayerExtent 获取输入图层的范围
func getInputLayerExtent(layer *GDALLayer) (*Extent, error) {
	type OGREnvelope struct {
		MinX C.double
		MaxX C.double
		MinY C.double
		MaxY C.double
	}

	var extent OGREnvelope

	// 获取输入图层的范围
	err := C.OGR_L_GetExtent(layer.layer, (*C.OGREnvelope)(unsafe.Pointer(&extent)), 1)
	if err != C.OGRERR_NONE {
		return nil, fmt.Errorf("获取输入图层范围失败，错误代码: %d", int(err))
	}

	return &Extent{
		MinX: float64(extent.MinX),
		MaxX: float64(extent.MaxX),
		MinY: float64(extent.MinY),
		MaxY: float64(extent.MaxY),
	}, nil
}

// createClipResultLayer 创建裁剪结果图层
func createClipResultLayer(layer1, layer2 *GDALLayer, table1Name, table2Name string) (*GDALLayer, error) {
	layerName := C.CString("clip_result")
	defer C.free(unsafe.Pointer(layerName))

	// 获取空间参考系统 - 使用输入图层的SRS
	srs := layer1.GetSpatialRef()

	// 创建结果图层 - 保持输入图层的几何类型
	inputGeomType := C.OGR_L_GetGeomType(layer1.layer)
	resultLayerPtr := C.createMemoryLayer(layerName, inputGeomType, srs)
	if resultLayerPtr == nil {
		return nil, fmt.Errorf("创建结果图层失败")
	}

	resultLayer := &GDALLayer{layer: resultLayerPtr}
	runtime.SetFinalizer(resultLayer, (*GDALLayer).cleanup)

	// 根据策略添加字段定义 - 裁剪通常保留输入图层的字段
	err := addClipFieldsBasedOnStrategy(resultLayer, layer1, layer2, table1Name, table2Name)
	if err != nil {
		resultLayer.Close()
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	return resultLayer, nil
}

// addClipFieldsBasedOnStrategy 根据策略添加裁剪字段
func addClipFieldsBasedOnStrategy(resultLayer, layer1, layer2 *GDALLayer, table1Name, table2Name string) error {
	return addLayerFields(resultLayer, layer1, "")
}

// processClipTilesInParallelSafe 并行处理裁剪分块
func processClipTilesInParallelSafe(layer1Copies, layer2Copies []*GDALLayer, resultLayer *GDALLayer, tiles []*TileInfo, table1Name, table2Name string, config *ParallelGeosConfig) error {
	// 创建工作池
	tilesChan := make(chan *TileInfo, len(tiles))
	resultsChan := make(chan *TileResult, len(tiles))

	// 启动工作协程
	var wg sync.WaitGroup
	for i := 0; i < config.MaxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			processClipTileWorkerSafe(workerID, layer1Copies[workerID], layer2Copies[workerID], table1Name, table2Name, config.PrecisionConfig, tilesChan, resultsChan)
		}(i)
	}

	// 发送分块任务
	go func() {
		defer close(tilesChan)
		for _, tile := range tiles {
			tilesChan <- tile
		}
	}()

	// 收集结果
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// 使用边界要素去重的结果收集函数
	return collectAndMergeResultsWithBorderDeduplication(resultsChan, resultLayer, len(tiles), config.ProgressCallback)
}

// processClipTileWorkerSafe 裁剪分块工作协程
func processClipTileWorkerSafe(workerID int, layer1Copy, layer2Copy *GDALLayer, table1Name, table2Name string, precisionConfig *GeometryPrecisionConfig, tilesChan <-chan *TileInfo, resultsChan chan<- *TileResult) {
	for tile := range tilesChan {
		startTime := time.Now()

		result := &TileResult{
			TileIndex:        tile.Index,
			InteriorFeatures: make([]C.OGRFeatureH, 0),
			BorderFeatures:   make([]C.OGRFeatureH, 0),
		}

		// 处理单个分块，传入精度配置
		interiorFeatures, borderFeatures, err := processSingleClipTileWithBorderDetection(layer1Copy, layer2Copy, tile, workerID, table1Name, table2Name, precisionConfig)
		if err != nil {
			result.Error = fmt.Errorf("工作协程 %d 处理裁剪分块 %d 失败: %v", workerID, tile.Index, err)
		} else {
			result.InteriorFeatures = interiorFeatures
			result.BorderFeatures = borderFeatures
		}

		result.ProcessTime = time.Since(startTime)
		resultsChan <- result

		// 清理分块几何体
		if tile.Envelope != nil {
			C.OGR_G_DestroyGeometry(tile.Envelope)
			tile.Envelope = nil
		}
	}
}

// processSingleClipTileWithBorderDetection 处理单个裁剪分块并检测边界要素
func processSingleClipTileWithBorderDetection(
	layer1Copy, layer2Copy *GDALLayer, tile *TileInfo, workerID int, table1Name, table2Name string, precisionConfig *GeometryPrecisionConfig) (
	[]C.OGRFeatureH, []C.OGRFeatureH, error) {

	// 创建当前分块的临时图层
	tempLayer1Name := C.CString(fmt.Sprintf("temp1_worker%d_tile%d", workerID, tile.Index))
	tempLayer2Name := C.CString(fmt.Sprintf("temp2_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(tempLayer1Name))
	defer C.free(unsafe.Pointer(tempLayer2Name))

	srs := layer1Copy.GetSpatialRef()
	tempLayer1Ptr := C.cloneLayerToMemory(layer1Copy.layer, tempLayer1Name)
	tempLayer2Ptr := C.cloneLayerToMemory(layer2Copy.layer, tempLayer2Name)

	if tempLayer1Ptr == nil || tempLayer2Ptr == nil {
		if tempLayer1Ptr != nil {
			C.OGR_L_Dereference(tempLayer1Ptr)
		}
		if tempLayer2Ptr != nil {
			C.OGR_L_Dereference(tempLayer2Ptr)
		}
		return nil, nil, fmt.Errorf("创建临时分块图层失败")
	}
	defer C.OGR_L_Dereference(tempLayer1Ptr)
	defer C.OGR_L_Dereference(tempLayer2Ptr)

	// 复制过滤后的要素 - 输入图层使用空间过滤，裁剪图层可能需要更大的范围
	count1 := C.copyFeaturesWithSpatialFilter(layer1Copy.layer, tempLayer1Ptr, tile.Envelope)
	count2 := C.copyFeaturesWithSpatialFilter(layer2Copy.layer, tempLayer2Ptr, tile.Envelope)

	if count1 == 0 {
		// 输入图层没有要素，返回空结果
		return []C.OGRFeatureH{}, []C.OGRFeatureH{}, nil
	}

	if count2 == 0 {
		// 裁剪图层没有要素，可能需要保留原始要素或返回空结果
		// 这里选择返回空结果，因为没有裁剪边界
		return []C.OGRFeatureH{}, []C.OGRFeatureH{}, nil
	}

	// 如果启用了精度设置，对临时图层进行精度处理
	if precisionConfig != nil && precisionConfig.Enabled && precisionConfig.GridSize > 0 {
		flags := precisionConfig.getFlags()
		gridSize := C.double(precisionConfig.GridSize)

		// 处理两个临时图层的几何精度
		processedCount1 := C.setLayerGeometryPrecision(tempLayer1Ptr, gridSize, flags)
		processedCount2 := C.setLayerGeometryPrecision(tempLayer2Ptr, gridSize, flags)

		fmt.Printf("分块 %d: 精度处理完成 - 输入图层: %d 要素, 裁剪图层: %d 要素\n",
			tile.Index, int(processedCount1), int(processedCount2))
	}

	// 创建结果临时图层
	resultTempName := C.CString(fmt.Sprintf("clip_result_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(resultTempName))

	// 保持输入图层的几何类型
	inputGeomType := C.OGR_L_GetGeomType(tempLayer1Ptr)
	resultTempPtr := C.createMemoryLayer(resultTempName, inputGeomType, srs)
	if resultTempPtr == nil {
		return nil, nil, fmt.Errorf("创建结果临时图层失败")
	}
	defer C.OGR_L_Dereference(resultTempPtr)

	tempLayer1 := &GDALLayer{layer: tempLayer1Ptr}
	tempLayer2 := &GDALLayer{layer: tempLayer2Ptr}
	resultTemp := &GDALLayer{layer: resultTempPtr}

	// 执行裁剪分析
	err := executeClipWithStrategy(tempLayer1, tempLayer2, resultTemp, table1Name, table2Name, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("分块裁剪分析失败: %v", err)
	}

	// 分类收集结果要素：内部要素和边界要素
	interiorFeatures := make([]C.OGRFeatureH, 0)
	borderFeatures := make([]C.OGRFeatureH, 0)

	resultTemp.ResetReading()
	bufferDistance := C.double(0.001)

	resultTemp.IterateFeatures(func(feature C.OGRFeatureH) {
		isOnBorder := C.isFeatureOnBorder(feature,
			C.double(tile.MinX), C.double(tile.MinY),
			C.double(tile.MaxX), C.double(tile.MaxY),
			bufferDistance)

		clonedFeature := C.OGR_F_Clone(feature)
		if clonedFeature != nil {
			if isOnBorder == 1 {
				borderFeatures = append(borderFeatures, clonedFeature)
			} else {
				interiorFeatures = append(interiorFeatures, clonedFeature)
			}
		}
	})

	return interiorFeatures, borderFeatures, nil
}

// executeClipWithStrategy 根据策略执行裁剪分析
func executeClipWithStrategy(inputLayer, clipLayer, resultLayer *GDALLayer, table1Name, table2Name string, progressCallback ProgressCallback) error {
	var options **C.char
	defer func() {
		if options != nil {
			C.CSLDestroy(options)
		}
	}()

	// 基本选项
	skipFailuresOpt := C.CString("SKIP_FAILURES=YES")
	promoteToMultiOpt := C.CString("PROMOTE_TO_MULTI=YES")
	defer C.free(unsafe.Pointer(skipFailuresOpt))
	defer C.free(unsafe.Pointer(promoteToMultiOpt))

	options = C.CSLAddString(options, skipFailuresOpt)
	options = C.CSLAddString(options, promoteToMultiOpt)

	return executeGDALClipWithProgress(inputLayer, clipLayer, resultLayer, options, progressCallback)
}

// executeGDALClipWithProgress 带进度监测的GDAL裁剪分析
func executeGDALClipWithProgress(inputLayer, clipLayer, resultLayer *GDALLayer, options **C.char, progressCallback ProgressCallback) error {
	var progressData *ProgressData
	var progressArg unsafe.Pointer

	// 启用多线程处理
	C.CPLSetConfigOption(C.CString("GDAL_NUM_THREADS"), C.CString("ALL_CPUS"))
	defer C.CPLSetConfigOption(C.CString("GDAL_NUM_THREADS"), nil)

	// 如果有进度回调，设置进度数据
	if progressCallback != nil {
		progressData = &ProgressData{
			callback:  progressCallback,
			cancelled: false,
		}

		// 将进度数据存储到全局映射中
		progressArg = unsafe.Pointer(progressData)
		progressDataMutex.Lock()
		progressDataMap[uintptr(progressArg)] = progressData
		progressDataMutex.Unlock()

		// 确保在函数结束时清理进度数据
		defer func() {
			progressDataMutex.Lock()
			delete(progressDataMap, uintptr(progressArg))
			progressDataMutex.Unlock()
		}()
	}

	// 调用C函数执行裁剪分析
	err := C.performClipWithProgress(
		inputLayer.layer,
		clipLayer.layer,
		resultLayer.layer,
		options,
		progressArg,
	)

	if err != C.OGRERR_NONE {
		// 检查是否是用户取消导致的错误
		if progressData != nil {
			progressData.mutex.RLock()
			cancelled := progressData.cancelled
			progressData.mutex.RUnlock()
			if cancelled {
				return fmt.Errorf("操作被用户取消")
			}
		}
		return fmt.Errorf("GDAL裁剪分析失败，错误代码: %d", int(err))
	}

	return nil
}

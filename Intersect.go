/*
Copyright (C) 2025 [fmecool]

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/
package Gogeo

/*
#include "osgeo_utils.h"
// 执行带进度监测的相交分析
static OGRErr performIntersectionWithProgress(OGRLayerH inputLayer,
                                     OGRLayerH methodLayer,
                                     OGRLayerH resultLayer,
                                     char **options,
                                     void *progressData) {
    return OGR_L_Intersection(inputLayer, methodLayer, resultLayer, options,
                             progressCallback, progressData);
}
*/
import "C"

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

// 并行空间相交分析
func SpatialIntersectionAnalysis(layer1, layer2 *GDALLayer, strategy FieldMergeStrategy, config *ParallelGeosConfig) (*GeosAnalysisResult, error) {
	defer layer1.Close()
	defer layer2.Close()
	table1 := layer1.GetLayerName()
	table2 := layer2.GetLayerName()
	// 执行并行相交分析
	resultLayer, err := performParallelIntersectionAnalysis(layer1, layer2, table1, table2, strategy, config)
	if err != nil {
		return nil, fmt.Errorf("执行并行相交分析失败: %v", err)
	}

	// 计算结果数量
	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("使用字段策略: %s，并行分块数: %dx%d\n", strategy.String(), config.TileCount, config.TileCount)

	return &GeosAnalysisResult{
		OutputLayer: resultLayer,
		ResultCount: resultCount,
	}, nil
}

func performParallelIntersectionAnalysis(layer1, layer2 *GDALLayer, table1Name, table2Name string, strategy FieldMergeStrategy, config *ParallelGeosConfig) (*GDALLayer, error) {
	// 获取数据范围
	extent, err := getLayersExtent(layer1, layer2)
	if err != nil {
		return nil, fmt.Errorf("获取数据范围失败: %v", err)
	}

	// 创建分块
	tiles := createTiles(extent, config.TileCount, config.BufferDistance)
	fmt.Printf("创建了 %d 个分块进行并行处理\n", len(tiles))

	// 创建结果图层 - 简化版本，让GDAL自动处理字段
	resultLayer, err := createResultLayer(layer1, layer2, table1Name, table2Name, strategy)
	if err != nil {
		return nil, fmt.Errorf("创建结果图层失败: %v", err)
	}

	// 为每个工作线程创建图层副本
	layer1Copies, layer2Copies, err := createLayerCopies(layer1, layer2, config.MaxWorkers)
	if err != nil {
		return nil, fmt.Errorf("创建图层副本失败: %v", err)
	}
	defer cleanupLayerCopies(layer1Copies, layer2Copies)

	// 并行处理分块 - 修正参数调用，添加table1Name和table2Name参数
	err = processtilesInParallelSafe(layer1Copies, layer2Copies, resultLayer, tiles, strategy, table1Name, table2Name, config)
	if err != nil {
		return nil, fmt.Errorf("并行处理分块失败: %v", err)
	}

	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("并行相交分析完成，共生成 %d 个相交要素\n", resultCount)

	return resultLayer, nil
}

// createLayerCopies 线程安全的并发优化版本
func createLayerCopies(layer1, layer2 *GDALLayer, workerCount int) ([]*GDALLayer, []*GDALLayer, error) {
	layer1Copies := make([]*GDALLayer, workerCount)
	layer2Copies := make([]*GDALLayer, workerCount)

	// 首先检查原始图层是否有要素
	layer1Count := layer1.GetFeatureCount()
	layer2Count := layer2.GetFeatureCount()
	fmt.Printf("原始图层要素数量 - Layer1: %d, Layer2: %d\n", layer1Count, layer2Count)

	if layer1Count == 0 || layer2Count == 0 {
		return nil, nil, fmt.Errorf("源图层为空 - Layer1: %d 要素, Layer2: %d 要素", layer1Count, layer2Count)
	}

	// 预先获取空间参考系统，避免并发访问
	srs1 := layer1.GetSpatialRef()
	srs2 := layer2.GetSpatialRef()

	// 使用互斥锁保护GDAL操作
	var gdalMutex sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, workerCount*2)

	// 创建副本的辅助函数
	createLayerCopy := func(sourceLayer *GDALLayer, srs C.OGRSpatialReferenceH, copyIndex int, layerPrefix string, targetSlice []*GDALLayer) {
		defer wg.Done()

		// 在锁保护下执行GDAL操作
		gdalMutex.Lock()

		layerName := C.CString(fmt.Sprintf("%s_copy_%d", layerPrefix, copyIndex))
		layerCopyPtr := C.cloneLayerToMemory(sourceLayer.layer, layerName)
		C.free(unsafe.Pointer(layerName))

		if layerCopyPtr == nil {
			gdalMutex.Unlock()
			errChan <- fmt.Errorf("创建%s副本%d失败", layerPrefix, copyIndex)
			return
		}

		// 复制要素 - 这个操作也需要在锁保护下
		count := C.copyAllFeatures(sourceLayer.layer, layerCopyPtr)

		gdalMutex.Unlock() // 释放锁

		if count == 0 {
			fmt.Printf("警告：%s副本%d没有复制到任何要素\n", layerPrefix, copyIndex)

			// 清理失败的副本
			gdalMutex.Lock()
			C.OGR_L_Dereference(layerCopyPtr)
			gdalMutex.Unlock()

			errChan <- fmt.Errorf("%s副本%d复制要素失败", layerPrefix, copyIndex)
			return
		}

		targetSlice[copyIndex] = &GDALLayer{layer: layerCopyPtr}
		runtime.SetFinalizer(targetSlice[copyIndex], (*GDALLayer).cleanup)
	}

	// 并发创建layer1的副本
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go createLayerCopy(layer1, srs1, i, "layer1", layer1Copies)
	}

	// 并发创建layer2的副本
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go createLayerCopy(layer2, srs2, i, "layer2", layer2Copies)
	}

	// 等待所有goroutine完成
	wg.Wait()
	close(errChan)

	// 检查是否有错误发生
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		// 清理已创建的副本
		cleanupPartialCopies(layer1Copies, layer2Copies)
		return nil, nil, fmt.Errorf("创建图层副本时发生 %d 个错误，首个错误: %v", len(errors), errors[0])
	}

	// 验证所有副本都已成功创建
	for i := 0; i < workerCount; i++ {
		if layer1Copies[i] == nil || layer2Copies[i] == nil {
			cleanupPartialCopies(layer1Copies, layer2Copies)
			return nil, nil, fmt.Errorf("副本创建不完整，索引 %d", i)
		}
	}

	fmt.Printf("成功并发创建了 %d 个图层副本对\n", workerCount)
	return layer1Copies, layer2Copies, nil
}

// cleanupPartialCopies 清理部分创建的副本
func cleanupPartialCopies(layer1Copies, layer2Copies []*GDALLayer) {
	for _, layer := range layer1Copies {
		if layer != nil {
			layer.Close()
		}
	}
	for _, layer := range layer2Copies {
		if layer != nil {
			layer.Close()
		}
	}
}

// cleanupLayerCopies 清理图层副本
func cleanupLayerCopies(layer1Copies, layer2Copies []*GDALLayer) {
	cleanupPartialCopies(layer1Copies, layer2Copies)
}

// 修改主要的并行处理函数
func processtilesInParallelSafe(layer1Copies, layer2Copies []*GDALLayer, resultLayer *GDALLayer, tiles []*TileInfo, strategy FieldMergeStrategy, table1Name, table2Name string, config *ParallelGeosConfig) error {
	// 创建工作池
	tilesChan := make(chan *TileInfo, len(tiles))
	resultsChan := make(chan *TileResult, len(tiles))

	// 启动工作协程
	var wg sync.WaitGroup
	for i := 0; i < config.MaxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// 修正参数调用，添加table1Name和table2Name参数
			processTileWorkerSafe(workerID, layer1Copies[workerID], layer2Copies[workerID], strategy, table1Name, table2Name, config.PrecisionConfig, tilesChan, resultsChan)

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

// 修改 processTileWorkerSafe 函数
func processTileWorkerSafe(workerID int, layer1Copy, layer2Copy *GDALLayer, strategy FieldMergeStrategy, table1Name, table2Name string, precisionConfig *GeometryPrecisionConfig, tilesChan <-chan *TileInfo, resultsChan chan<- *TileResult) {
	for tile := range tilesChan {
		startTime := time.Now()

		result := &TileResult{
			TileIndex:        tile.Index,
			InteriorFeatures: make([]C.OGRFeatureH, 0),
			BorderFeatures:   make([]C.OGRFeatureH, 0),
		}

		// 处理单个分块，传入精度配置
		interiorFeatures, borderFeatures, err := processSingleTileWithBorderDetection(layer1Copy, layer2Copy, tile, strategy, workerID, table1Name, table2Name, precisionConfig)
		if err != nil {
			result.Error = fmt.Errorf("工作协程 %d 处理分块 %d 失败: %v", workerID, tile.Index, err)
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

// 修改 函数
func processSingleTileWithBorderDetection(
	layer1Copy, layer2Copy *GDALLayer, tile *TileInfo, strategy FieldMergeStrategy, workerID int, table1Name, table2Name string, precisionConfig *GeometryPrecisionConfig) (
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

	// 复制过滤后的要素
	count1 := C.copyFeaturesWithSpatialFilter(layer1Copy.layer, tempLayer1Ptr, tile.Envelope)
	count2 := C.copyFeaturesWithSpatialFilter(layer2Copy.layer, tempLayer2Ptr, tile.Envelope)

	if count1 == 0 || count2 == 0 {
		return []C.OGRFeatureH{}, []C.OGRFeatureH{}, nil
	}

	// 如果启用了精度设置，对临时图层进行精度处理
	if precisionConfig != nil && precisionConfig.Enabled && precisionConfig.GridSize > 0 {
		flags := precisionConfig.getFlags()
		gridSize := C.double(precisionConfig.GridSize)

		// 处理两个临时图层的几何精度
		processedCount1 := C.setLayerGeometryPrecision(tempLayer1Ptr, gridSize, flags)
		processedCount2 := C.setLayerGeometryPrecision(tempLayer2Ptr, gridSize, flags)

		fmt.Printf("分块 %d: 精度处理完成 - Layer1: %d 要素, Layer2: %d 要素\n",
			tile.Index, int(processedCount1), int(processedCount2))
	}

	// 创建结果临时图层
	resultTempName := C.CString(fmt.Sprintf("result_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(resultTempName))

	resultTempPtr := C.createMemoryLayer(resultTempName, C.wkbMultiPolygon, srs)
	if resultTempPtr == nil {
		return nil, nil, fmt.Errorf("创建结果临时图层失败")
	}
	defer C.OGR_L_Dereference(resultTempPtr)

	tempLayer1 := &GDALLayer{layer: tempLayer1Ptr}
	tempLayer2 := &GDALLayer{layer: tempLayer2Ptr}
	resultTemp := &GDALLayer{layer: resultTempPtr}

	// 执行相交分析
	err := executeIntersectionWithStrategy(tempLayer1, tempLayer2, resultTemp, table1Name, table2Name, strategy, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("分块相交分析失败: %v", err)
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

func executeIntersectionWithStrategy(layer1, layer2, resultLayer *GDALLayer, table1Name, table2Name string, strategy FieldMergeStrategy, progressCallback ProgressCallback) error {
	var options **C.char
	defer func() {
		if options != nil {
			C.CSLDestroy(options)
		}
	}()

	switch strategy {
	case UseTable1Fields:
		// 只保留输入图层的字段
		skipFailuresOpt := C.CString("SKIP_FAILURES=YES")
		promoteToMultiOpt := C.CString("PROMOTE_TO_MULTI=YES")
		inputFieldsOpt := C.CString("INPUT_FIELDS_ONLY=YES")
		keepLowerDimOpt := C.CString("KEEP_LOWER_DIMENSION_GEOMETRIES=NO")
		defer C.free(unsafe.Pointer(skipFailuresOpt))
		defer C.free(unsafe.Pointer(promoteToMultiOpt))
		defer C.free(unsafe.Pointer(inputFieldsOpt))
		defer C.free(unsafe.Pointer(keepLowerDimOpt))

		options = C.CSLAddString(options, skipFailuresOpt)
		options = C.CSLAddString(options, promoteToMultiOpt)
		options = C.CSLAddString(options, inputFieldsOpt)
		options = C.CSLAddString(options, keepLowerDimOpt)

		return executeGDALIntersectionWithProgress(layer1, layer2, resultLayer, options, progressCallback)

	case UseTable2Fields:
		// 只保留方法图层的字段
		skipFailuresOpt := C.CString("SKIP_FAILURES=YES")
		promoteToMultiOpt := C.CString("PROMOTE_TO_MULTI=YES")
		methodFieldsOpt := C.CString("METHOD_FIELDS_ONLY=YES")
		keepLowerDimOpt := C.CString("KEEP_LOWER_DIMENSION_GEOMETRIES=NO")
		defer C.free(unsafe.Pointer(skipFailuresOpt))
		defer C.free(unsafe.Pointer(promoteToMultiOpt))
		defer C.free(unsafe.Pointer(methodFieldsOpt))
		defer C.free(unsafe.Pointer(keepLowerDimOpt))

		options = C.CSLAddString(options, skipFailuresOpt)
		options = C.CSLAddString(options, promoteToMultiOpt)
		options = C.CSLAddString(options, methodFieldsOpt)
		options = C.CSLAddString(options, keepLowerDimOpt)

		return executeGDALIntersectionWithProgress(layer1, layer2, resultLayer, options, progressCallback)

	case MergePreferTable1:
		// 合并字段，冲突时优先使用输入图层
		skipFailuresOpt := C.CString("SKIP_FAILURES=YES")
		promoteToMultiOpt := C.CString("PROMOTE_TO_MULTI=YES")
		keepLowerDimOpt := C.CString("KEEP_LOWER_DIMENSION_GEOMETRIES=NO")
		defer C.free(unsafe.Pointer(skipFailuresOpt))
		defer C.free(unsafe.Pointer(promoteToMultiOpt))
		defer C.free(unsafe.Pointer(keepLowerDimOpt))

		options = C.CSLAddString(options, skipFailuresOpt)
		options = C.CSLAddString(options, promoteToMultiOpt)
		options = C.CSLAddString(options, keepLowerDimOpt)

		// 默认行为就是输入图层优先
		return executeGDALIntersectionWithProgress(layer1, layer2, resultLayer, options, progressCallback)

	case MergePreferTable2:
		// 合并字段，冲突时优先使用方法图层 - 交换图层顺序
		skipFailuresOpt := C.CString("SKIP_FAILURES=YES")
		promoteToMultiOpt := C.CString("PROMOTE_TO_MULTI=YES")
		keepLowerDimOpt := C.CString("KEEP_LOWER_DIMENSION_GEOMETRIES=NO")
		defer C.free(unsafe.Pointer(skipFailuresOpt))
		defer C.free(unsafe.Pointer(promoteToMultiOpt))
		defer C.free(unsafe.Pointer(keepLowerDimOpt))

		options = C.CSLAddString(options, skipFailuresOpt)
		options = C.CSLAddString(options, promoteToMultiOpt)
		options = C.CSLAddString(options, keepLowerDimOpt)

		// 交换图层顺序，让table2作为输入图层
		return executeGDALIntersectionWithProgress(layer2, layer1, resultLayer, options, progressCallback)

	case MergeWithPrefix:
		// 使用前缀区分字段来源
		skipFailuresOpt := C.CString("SKIP_FAILURES=YES")
		promoteToMultiOpt := C.CString("PROMOTE_TO_MULTI=YES")
		inputPrefixOpt := C.CString(fmt.Sprintf("INPUT_PREFIX=%s_", table1Name))
		methodPrefixOpt := C.CString(fmt.Sprintf("METHOD_PREFIX=%s_", table2Name))
		keepLowerDimOpt := C.CString("KEEP_LOWER_DIMENSION_GEOMETRIES=NO")
		defer C.free(unsafe.Pointer(skipFailuresOpt))
		defer C.free(unsafe.Pointer(promoteToMultiOpt))
		defer C.free(unsafe.Pointer(inputPrefixOpt))
		defer C.free(unsafe.Pointer(methodPrefixOpt))
		defer C.free(unsafe.Pointer(keepLowerDimOpt))

		options = C.CSLAddString(options, skipFailuresOpt)
		options = C.CSLAddString(options, promoteToMultiOpt)
		options = C.CSLAddString(options, inputPrefixOpt)
		options = C.CSLAddString(options, methodPrefixOpt)
		options = C.CSLAddString(options, keepLowerDimOpt)

		return executeGDALIntersectionWithProgress(layer1, layer2, resultLayer, options, progressCallback)

	default:
		return fmt.Errorf("不支持的字段合并策略: %v", strategy)
	}
}

func createResultLayer(layer1, layer2 *GDALLayer, table1Name, table2Name string, strategy FieldMergeStrategy) (*GDALLayer, error) {
	layerName := C.CString("intersection_result")
	defer C.free(unsafe.Pointer(layerName))

	// 获取空间参考系统
	srs := layer1.GetSpatialRef()
	if srs == nil {
		srs = layer2.GetSpatialRef()
	}

	// 创建结果图层
	resultLayerPtr := C.createMemoryLayer(layerName, C.wkbMultiPolygon, srs)
	if resultLayerPtr == nil {
		return nil, fmt.Errorf("创建结果图层失败")
	}

	resultLayer := &GDALLayer{layer: resultLayerPtr}
	runtime.SetFinalizer(resultLayer, (*GDALLayer).cleanup)

	// 根据策略添加字段定义
	err := addFieldsBasedOnStrategy(resultLayer, layer1, layer2, table1Name, table2Name, strategy)
	if err != nil {
		resultLayer.Close()
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	return resultLayer, nil
}

// 新增函数：根据策略添加字段
func addFieldsBasedOnStrategy(resultLayer, layer1, layer2 *GDALLayer, table1Name, table2Name string, strategy FieldMergeStrategy) error {

	switch strategy {
	case UseTable1Fields:
		// 只添加table1的字段
		return addLayerFields(resultLayer, layer1, "")

	case UseTable2Fields:
		// 只添加table2的字段
		return addLayerFields(resultLayer, layer2, "")

	case MergePreferTable1:
		// 先添加table1字段，再添加table2中不冲突的字段
		if err := addLayerFields(resultLayer, layer1, ""); err != nil {
			return err
		}
		return addNonConflictingFields(resultLayer, layer2, layer1, "")

	case MergePreferTable2:
		// 先添加table2字段，再添加table1中不冲突的字段
		if err := addLayerFields(resultLayer, layer2, ""); err != nil {
			return err
		}
		return addNonConflictingFields(resultLayer, layer1, layer2, "")

	case MergeWithPrefix:
		// 添加带前缀的字段
		if err := addLayerFields(resultLayer, layer1, table1Name+"_"); err != nil {
			return err
		}
		return addLayerFields(resultLayer, layer2, table2Name+"_")

	default:
		return fmt.Errorf("不支持的字段策略: %v", strategy)
	}
}

// 添加不冲突的字段
func addNonConflictingFields(resultLayer, sourceLayer, existingLayer *GDALLayer, prefix string) error {
	sourceDefn := C.OGR_L_GetLayerDefn(sourceLayer.layer)
	resultDefn := C.OGR_L_GetLayerDefn(resultLayer.layer)

	fieldCount := int(C.OGR_FD_GetFieldCount(sourceDefn))

	for i := 0; i < fieldCount; i++ {
		fieldDefn := C.OGR_FD_GetFieldDefn(sourceDefn, C.int(i))
		fieldName := C.OGR_Fld_GetNameRef(fieldDefn)
		fieldNameStr := C.GoString(fieldName)

		// 检查字段是否已存在
		fieldIndex := C.OGR_FD_GetFieldIndex(resultDefn, fieldName)
		if fieldIndex >= 0 {
			// 字段已存在，跳过
			continue
		}

		// 添加字段
		var newFieldName string
		if prefix != "" {
			newFieldName = prefix + fieldNameStr
		} else {
			newFieldName = fieldNameStr
		}

		newFieldNameC := C.CString(newFieldName)
		newFieldDefn := C.OGR_Fld_Create(newFieldNameC, C.OGR_Fld_GetType(fieldDefn))

		C.OGR_Fld_SetWidth(newFieldDefn, C.OGR_Fld_GetWidth(fieldDefn))
		C.OGR_Fld_SetPrecision(newFieldDefn, C.OGR_Fld_GetPrecision(fieldDefn))

		err := C.OGR_L_CreateField(resultLayer.layer, newFieldDefn, 1)

		C.OGR_Fld_Destroy(newFieldDefn)
		C.free(unsafe.Pointer(newFieldNameC))

		if err != C.OGRERR_NONE {
			return fmt.Errorf("创建字段 %s 失败", newFieldName)
		}
	}

	return nil
}

// getLayersExtent 获取两个图层的合并范围
func getLayersExtent(layer1, layer2 *GDALLayer) (*Extent, error) {
	// 定义 OGREnvelope 结构体
	type OGREnvelope struct {
		MinX C.double
		MaxX C.double
		MinY C.double
		MaxY C.double
	}

	var extent1, extent2 OGREnvelope

	// 获取第一个图层的范围
	err := C.OGR_L_GetExtent(layer1.layer, (*C.OGREnvelope)(unsafe.Pointer(&extent1)), 1)
	if err != C.OGRERR_NONE {
		return nil, fmt.Errorf("获取图层1范围失败，错误代码: %d", int(err))
	}

	// 获取第二个图层的范围
	err = C.OGR_L_GetExtent(layer2.layer, (*C.OGREnvelope)(unsafe.Pointer(&extent2)), 1)
	if err != C.OGRERR_NONE {
		return nil, fmt.Errorf("获取图层2范围失败，错误代码: %d", int(err))
	}

	// 计算合并范围
	return &Extent{
		MinX: math.Min(float64(extent1.MinX), float64(extent2.MinX)),
		MaxX: math.Max(float64(extent1.MaxX), float64(extent2.MaxX)),
		MinY: math.Min(float64(extent1.MinY), float64(extent2.MinY)),
		MaxY: math.Max(float64(extent1.MaxY), float64(extent2.MaxY)),
	}, nil
}

// Extent 空间范围结构
type Extent struct {
	MinX, MinY, MaxX, MaxY float64
}

// createTiles 创建分块
func createTiles(extent *Extent, tileCount int, bufferDistance float64) []*TileInfo {
	tiles := make([]*TileInfo, 0, tileCount*tileCount)

	tileWidth := (extent.MaxX - extent.MinX) / float64(tileCount)
	tileHeight := (extent.MaxY - extent.MinY) / float64(tileCount)

	index := 0
	for i := 0; i < tileCount; i++ {
		for j := 0; j < tileCount; j++ {
			minX := extent.MinX + float64(j)*tileWidth - bufferDistance
			maxX := extent.MinX + float64(j+1)*tileWidth + bufferDistance
			minY := extent.MinY + float64(i)*tileHeight - bufferDistance
			maxY := extent.MinY + float64(i+1)*tileHeight + bufferDistance

			// 创建分块包络几何体
			envelope := createEnvelopeGeometry(minX, minY, maxX, maxY)

			tiles = append(tiles, &TileInfo{
				Index:    index,
				MinX:     minX,
				MinY:     minY,
				MaxX:     maxX,
				MaxY:     maxY,
				Envelope: envelope,
			})
			index++
		}
	}

	return tiles
}

// createEnvelopeGeometry 创建包络几何体
func createEnvelopeGeometry(minX, minY, maxX, maxY float64) C.OGRGeometryH {
	// 创建多边形几何体
	ring := C.OGR_G_CreateGeometry(C.wkbLinearRing)
	C.OGR_G_AddPoint_2D(ring, C.double(minX), C.double(minY))
	C.OGR_G_AddPoint_2D(ring, C.double(maxX), C.double(minY))
	C.OGR_G_AddPoint_2D(ring, C.double(maxX), C.double(maxY))
	C.OGR_G_AddPoint_2D(ring, C.double(minX), C.double(maxY))
	C.OGR_G_AddPoint_2D(ring, C.double(minX), C.double(minY))

	polygon := C.OGR_G_CreateGeometry(C.wkbPolygon)
	C.OGR_G_AddGeometry(polygon, ring)
	C.OGR_G_DestroyGeometry(ring)

	return polygon
}

// executeGDALIntersectionWithProgress 带进度监测的GDAL相交分析
func executeGDALIntersectionWithProgress(inputLayer, methodLayer, resultLayer *GDALLayer, options **C.char, progressCallback ProgressCallback) error {
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

	// 调用C函数执行相交分析
	err := C.performIntersectionWithProgress(
		inputLayer.layer,
		methodLayer.layer,
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
		return fmt.Errorf("GDAL相交分析失败，错误代码: %d", int(err))
	}

	return nil
}

// 修改结果收集函数，只对边界要素进行去重
func collectAndMergeResultsWithBorderDeduplication(resultsChan <-chan *TileResult, resultLayer *GDALLayer, totalTiles int, progressCallback ProgressCallback) error {
	processedTiles := 0
	var totalProcessTime time.Duration

	resultMutex := sync.Mutex{}

	// 存储边界要素用于去重
	borderFeaturesMap := make(map[string]*BorderFeatureInfo)

	// 首先收集所有结果
	for result := range resultsChan {
		if result.Error != nil {
			// 清理资源
			cleanupTileResult(result)
			cleanupBorderFeaturesMap(borderFeaturesMap)
			return result.Error
		}

		resultMutex.Lock()

		// 直接添加内部要素（不需要去重）
		for _, feature := range result.InteriorFeatures {
			if feature != nil {
				C.OGR_L_CreateFeature(resultLayer.layer, feature)
				C.OGR_F_Destroy(feature)
			}
		}

		// 收集边界要素用于去重
		for _, feature := range result.BorderFeatures {
			if feature != nil {
				wkt := getFeatureGeometryWKT(feature)
				if wkt != "" {
					if existing, exists := borderFeaturesMap[wkt]; exists {
						// 已存在相同几何体的要素，记录分块索引
						existing.TileIndices = append(existing.TileIndices, result.TileIndex)
						C.OGR_F_Destroy(feature) // 销毁重复的要素
					} else {
						// 新的边界要素
						borderFeaturesMap[wkt] = &BorderFeatureInfo{
							Feature:     feature,
							TileIndices: []int{result.TileIndex},
							GeometryWKT: wkt,
						}
					}
				}
			}
		}

		resultMutex.Unlock()

		processedTiles++
		totalProcessTime += result.ProcessTime

		// 更新进度
		if progressCallback != nil {
			progress := float64(processedTiles) / float64(totalTiles) * 0.9 // 为去重预留10%进度
			message := fmt.Sprintf("已完成分块 %d/%d，内部要素直接添加，边界要素待去重",
				processedTiles, totalTiles)

			if !progressCallback(progress, message) {
				cleanupBorderFeaturesMap(borderFeaturesMap)
				return fmt.Errorf("操作被用户取消")
			}
		}
	}

	// 添加去重后的边界要素
	err := addDeduplicatedBorderFeatures(borderFeaturesMap, resultLayer, progressCallback)
	if err != nil {
		cleanupBorderFeaturesMap(borderFeaturesMap)
		return err
	}

	// 统计信息
	totalBorderFeatures := len(borderFeaturesMap)
	duplicateCount := 0
	for _, info := range borderFeaturesMap {
		if len(info.TileIndices) > 1 {
			duplicateCount += len(info.TileIndices) - 1
		}
	}

	fmt.Printf("边界要素去重完成: 唯一边界要素 %d 个，移除重复 %d 个\n",
		totalBorderFeatures, duplicateCount)
	fmt.Printf("总处理时间: %v，平均每分块: %v\n",
		totalProcessTime, totalProcessTime/time.Duration(totalTiles))

	return nil
}

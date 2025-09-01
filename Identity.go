package Gogeo

/*
#include "osgeo_utils.h"
static OGRErr performIdentityWithProgress(OGRLayerH inputLayer,
                                     OGRLayerH methodLayer,
                                     OGRLayerH resultLayer,
                                     char **options,
                                     void *progressData) {
    return OGR_L_Identity(inputLayer, methodLayer, resultLayer, options,
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

// TileIdentityInfo 瓦片Identity信息
type TileIdentityInfo struct {
	Index int
	MinX  float64
	MinY  float64
	MaxX  float64
	MaxY  float64
}

// TileIdentityResult 瓦片Identity结果
type TileIdentityResult struct {
	TileIndex   int
	Features    []C.OGRFeatureH
	ProcessTime time.Duration
	Error       error
}

func SpatialIdentityAnalysis(inputLayer, methodLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error) {

	defer inputLayer.Close()

	defer methodLayer.Close()
	inputTable := inputLayer.GetLayerName()
	methodTable := methodLayer.GetLayerName()
	// 为两个图层添加唯一标识字段
	err := addUniqueIdentityIdentifierFields(inputLayer, methodLayer, inputTable, methodTable)
	if err != nil {
		return nil, fmt.Errorf("添加唯一标识字段失败: %v", err)
	}

	// 执行基于瓦片裁剪的并行Identity分析
	resultLayer, err := performTileClipIdentityAnalysis(inputLayer, methodLayer, inputTable, methodTable, config)
	if err != nil {
		return nil, fmt.Errorf("执行瓦片裁剪Identity分析失败: %v", err)
	}

	// 计算结果数量
	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("瓦片裁剪并行分块数: %dx%d\n", config.TileCount, config.TileCount)
	fmt.Printf("Identity分析完成，共生成 %d 个要素\n", resultCount)

	if config.IsMergeTile {
		// 执行按标识字段的融合操作
		unionResult, err := performIdentityUnionByIdentifierFields(resultLayer, inputTable, methodTable, config.PrecisionConfig, config.ProgressCallback)
		if err != nil {
			return nil, fmt.Errorf("执行融合操作失败: %v", err)
		}

		fmt.Printf("融合操作完成，最终生成 %d 个要素\n", unionResult.ResultCount)
		// 删除临时的_identityID字段
		err = removeTempIdentityIdentifierFields(unionResult.OutputLayer, inputTable, methodTable)
		if err != nil {
			fmt.Printf("警告: 删除临时标识字段失败: %v\n", err)
		} else {
			fmt.Printf("成功删除临时标识字段: %s_identityID, %s_identityID\n", inputTable, methodTable)
		}

		return unionResult, nil
	} else {

		return &GeosAnalysisResult{
			OutputLayer: resultLayer,
			ResultCount: resultCount,
		}, nil
	}

}

// removeTempIdentityIdentifierFields 删除临时的_identityID字段
func removeTempIdentityIdentifierFields(resultLayer *GDALLayer, inputTableName, methodTableName string) error {
	field1Name := inputTableName + "_identityID"
	field2Name := methodTableName + "_identityID"

	err1 := deleteFieldFromLayer(resultLayer, field1Name)
	if err1 != nil {
		return fmt.Errorf("删除字段 %s 失败: %v", field1Name, err1)
	}

	err2 := deleteFieldFromLayer(resultLayer, field2Name)
	if err2 != nil {
		return fmt.Errorf("删除字段 %s 失败: %v", field2Name, err2)
	}

	return nil
}

// addUniqueIdentityIdentifierFields 为Identity操作添加唯一标识字段
func addUniqueIdentityIdentifierFields(inputLayer, methodLayer *GDALLayer, inputTableName, methodTableName string) error {
	// 为输入图层创建带标识字段的副本
	newInputLayer, err := createLayerWithIdentityIdentifierField(inputLayer, inputTableName+"_identityID")
	if err != nil {
		return fmt.Errorf("为输入图层 %s 创建带标识字段的副本失败: %v", inputTableName, err)
	}

	// 为方法图层创建带标识字段的副本
	newMethodLayer, err := createLayerWithIdentityIdentifierField(methodLayer, methodTableName+"_identityID")
	if err != nil {
		return fmt.Errorf("为方法图层 %s 创建带标识字段的副本失败: %v", methodTableName, err)
	}

	// 替换原始图层
	inputLayer.Close()
	methodLayer.Close()
	*inputLayer = *newInputLayer
	*methodLayer = *newMethodLayer

	fmt.Printf("成功添加标识字段: %s_identityID, %s_identityID\n", inputTableName, methodTableName)
	return nil
}

// createLayerWithIdentityIdentifierField 创建一个带有Identity标识字段的新图层
func createLayerWithIdentityIdentifierField(sourceLayer *GDALLayer, identifierFieldName string) (*GDALLayer, error) {
	// 创建内存数据源
	driverName := C.CString("MEM")
	driver := C.OGRGetDriverByName(driverName)
	C.free(unsafe.Pointer(driverName))

	if driver == nil {
		return nil, fmt.Errorf("无法获取Memory驱动")
	}

	dataSourceName := C.CString("")
	dataSource := C.OGR_Dr_CreateDataSource(driver, dataSourceName, nil)
	C.free(unsafe.Pointer(dataSourceName))

	if dataSource == nil {
		return nil, fmt.Errorf("无法创建内存数据源")
	}

	// 获取源图层的空间参考系统
	sourceLayerDefn := C.OGR_L_GetLayerDefn(sourceLayer.layer)
	sourceSRS := C.OGR_L_GetSpatialRef(sourceLayer.layer)

	// 创建新图层
	layerName := C.CString("temp_identity_layer")
	geomType := C.OGR_FD_GetGeomType(sourceLayerDefn)
	newLayer := C.OGR_DS_CreateLayer(dataSource, layerName, sourceSRS, geomType, nil)
	C.free(unsafe.Pointer(layerName))

	if newLayer == nil {
		C.OGR_DS_Destroy(dataSource)
		return nil, fmt.Errorf("无法创建新图层")
	}

	// 复制原有字段定义
	fieldCount := C.OGR_FD_GetFieldCount(sourceLayerDefn)
	for i := C.int(0); i < fieldCount; i++ {
		fieldDefn := C.OGR_FD_GetFieldDefn(sourceLayerDefn, i)
		if C.OGR_L_CreateField(newLayer, fieldDefn, 1) != C.OGRERR_NONE {
			C.OGR_DS_Destroy(dataSource)
			return nil, fmt.Errorf("复制字段定义失败")
		}
	}

	// 添加标识字段
	identifierFieldNameC := C.CString(identifierFieldName)
	identifierFieldDefn := C.OGR_Fld_Create(identifierFieldNameC, C.OFTInteger64)
	defer C.OGR_Fld_Destroy(identifierFieldDefn)
	defer C.free(unsafe.Pointer(identifierFieldNameC))

	if C.OGR_L_CreateField(newLayer, identifierFieldDefn, 1) != C.OGRERR_NONE {
		C.OGR_DS_Destroy(dataSource)
		return nil, fmt.Errorf("创建标识字段失败")
	}

	// 获取标识字段索引
	newLayerDefn := C.OGR_L_GetLayerDefn(newLayer)
	identifierFieldIndex := C.OGR_FD_GetFieldIndex(newLayerDefn, identifierFieldNameC)

	// 复制要素并添加标识字段值
	sourceLayer.ResetReading()
	var featureID int64 = 1

	sourceLayer.IterateFeatures(func(sourceFeature C.OGRFeatureH) {
		// 创建新要素
		newFeature := C.OGR_F_Create(newLayerDefn)
		if newFeature == nil {
			return
		}
		defer C.OGR_F_Destroy(newFeature)

		// 复制几何体
		geom := C.OGR_F_GetGeometryRef(sourceFeature)
		if geom != nil {
			geomClone := C.OGR_G_Clone(geom)
			C.OGR_F_SetGeometry(newFeature, geomClone)
			C.OGR_G_DestroyGeometry(geomClone)
		}

		// 复制原有字段值
		for i := C.int(0); i < fieldCount; i++ {
			if C.OGR_F_IsFieldSet(sourceFeature, i) != 0 {
				sourceFieldDefn := C.OGR_FD_GetFieldDefn(sourceLayerDefn, i)
				fieldType := C.OGR_Fld_GetType(sourceFieldDefn)

				switch fieldType {
				case C.OFTInteger:
					value := C.OGR_F_GetFieldAsInteger(sourceFeature, i)
					C.OGR_F_SetFieldInteger(newFeature, i, value)
				case C.OFTInteger64:
					value := C.OGR_F_GetFieldAsInteger64(sourceFeature, i)
					C.OGR_F_SetFieldInteger64(newFeature, i, value)
				case C.OFTReal:
					value := C.OGR_F_GetFieldAsDouble(sourceFeature, i)
					C.OGR_F_SetFieldDouble(newFeature, i, value)
				case C.OFTString:
					value := C.OGR_F_GetFieldAsString(sourceFeature, i)
					C.OGR_F_SetFieldString(newFeature, i, value)
				case C.OFTDate, C.OFTTime, C.OFTDateTime:
					var year, month, day, hour, minute, second, tzflag C.int
					C.OGR_F_GetFieldAsDateTime(sourceFeature, i, &year, &month, &day, &hour, &minute, &second, &tzflag)
					C.OGR_F_SetFieldDateTime(newFeature, i, year, month, day, hour, minute, second, tzflag)
				default:
					value := C.OGR_F_GetFieldAsString(sourceFeature, i)
					C.OGR_F_SetFieldString(newFeature, i, value)
				}
			}
		}

		// 设置标识字段值
		C.OGR_F_SetFieldInteger64(newFeature, identifierFieldIndex, C.longlong(featureID))
		featureID++

		// 添加要素到新图层
		C.OGR_L_CreateFeature(newLayer, newFeature)
	})

	// 创建新的GDALLayer包装器
	result := &GDALLayer{
		layer:   newLayer,
		dataset: dataSource,
	}

	fmt.Printf("成功创建带标识字段的Identity图层，共处理 %d 个要素\n", featureID-1)
	return result, nil
}

// performIdentityUnionByIdentifierFields 执行按标识字段的Identity融合操作
func performIdentityUnionByIdentifierFields(inputLayer *GDALLayer, inputTableName, methodTableName string,
	precisionConfig *GeometryPrecisionConfig, progressCallback ProgressCallback) (*GeosAnalysisResult, error) {

	// 构建分组字段列表
	groupFields := []string{
		IdentitySourceIdentifierField,   // "source_layer" 字段
		inputTableName + "_identityID",  // 输入表的标识字段
		methodTableName + "_identityID", // 方法表的标识字段
	}

	// 构建输出图层名称
	outputLayerName := fmt.Sprintf("identity_union_%s_%s", inputTableName, methodTableName)

	// 为融合操作调整精度配置（放大网格大小）
	fusionPrecisionConfig := *precisionConfig
	fusionPrecisionConfig.GridSize = 0

	// 执行融合操作
	return UnionByFieldsWithPrecision(inputLayer, groupFields, outputLayerName, &fusionPrecisionConfig, progressCallback)
}

// performTileClipIdentityAnalysis 执行基于瓦片裁剪的并行Identity分析
func performTileClipIdentityAnalysis(inputLayer, methodLayer *GDALLayer, inputTableName, methodTableName string, config *ParallelGeosConfig) (*GDALLayer, error) {
	// 在分块裁剪前对两个图层进行精度处理
	if config.PrecisionConfig != nil && config.PrecisionConfig.Enabled && config.PrecisionConfig.GridSize > 0 {
		fmt.Printf("开始对图层进行精度处理，网格大小: %f\n", config.PrecisionConfig.GridSize)

		flags := config.PrecisionConfig.getFlags()
		gridSize := C.double(config.PrecisionConfig.GridSize)

		// 对输入图层进行精度处理
		C.setLayerGeometryPrecision(inputLayer.layer, gridSize, flags)
		fmt.Printf("输入图层 %s 精度处理完成\n", inputTableName)

		// 对方法图层进行精度处理
		C.setLayerGeometryPrecision(methodLayer.layer, gridSize, flags)
		fmt.Printf("方法图层 %s 精度处理完成\n", methodTableName)
	}

	// 获取数据范围
	extent, err := getLayersExtent(inputLayer, methodLayer)
	if err != nil {
		return nil, fmt.Errorf("获取数据范围失败: %v", err)
	}

	// 创建瓦片裁剪信息
	tiles := createTileIdentityInfos(extent, config.TileCount)
	fmt.Printf("创建了 %d 个瓦片进行Identity裁剪并行处理\n", len(tiles))

	// 创建结果图层
	resultLayer, err := createIdentityAnalysisResultLayer(inputLayer, methodLayer, inputTableName, methodTableName)
	if err != nil {
		return nil, fmt.Errorf("创建结果图层失败: %v", err)
	}

	// 为每个工作线程创建图层副本
	inputLayerCopies, methodLayerCopies, err := createLayerCopies(inputLayer, methodLayer, config.MaxWorkers)
	if err != nil {
		return nil, fmt.Errorf("创建图层副本失败: %v", err)
	}
	defer cleanupLayerCopies(inputLayerCopies, methodLayerCopies)

	// 并行处理瓦片裁剪
	err = processTileClipIdentityInParallel(inputLayerCopies, methodLayerCopies, resultLayer, tiles, inputTableName, methodTableName, config)
	if err != nil {
		return nil, fmt.Errorf("并行处理瓦片裁剪失败: %v", err)
	}

	resultCount := resultLayer.GetFeatureCount()
	fmt.Printf("瓦片裁剪Identity分析完成，共生成 %d 个要素\n", resultCount)

	return resultLayer, nil
}

// createTileIdentityInfos 创建瓦片Identity信息
func createTileIdentityInfos(extent *Extent, tileCount int) []*TileIdentityInfo {
	tiles := make([]*TileIdentityInfo, 0, tileCount*tileCount)

	width := extent.MaxX - extent.MinX
	height := extent.MaxY - extent.MinY

	tileWidth := width / float64(tileCount)
	tileHeight := height / float64(tileCount)

	index := 0
	for row := 0; row < tileCount; row++ {
		for col := 0; col < tileCount; col++ {
			minX := extent.MinX + float64(col)*tileWidth
			maxX := extent.MinX + float64(col+1)*tileWidth
			minY := extent.MinY + float64(row)*tileHeight
			maxY := extent.MinY + float64(row+1)*tileHeight

			// 确保最后一行/列覆盖到边界
			if col == tileCount-1 {
				maxX = extent.MaxX
			}
			if row == tileCount-1 {
				maxY = extent.MaxY
			}

			tiles = append(tiles, &TileIdentityInfo{
				Index: index,
				MinX:  minX,
				MinY:  minY,
				MaxX:  maxX,
				MaxY:  maxY,
			})
			index++
		}
	}

	return tiles
}

// processTileClipIdentityInParallel 并行处理瓦片裁剪Identity
func processTileClipIdentityInParallel(inputLayerCopies, methodLayerCopies []*GDALLayer, resultLayer *GDALLayer, tiles []*TileIdentityInfo, inputTableName, methodTableName string, config *ParallelGeosConfig) error {
	// 创建工作池
	tilesChan := make(chan *TileIdentityInfo, len(tiles))
	resultsChan := make(chan *TileIdentityResult, len(tiles))

	// 启动工作协程
	var wg sync.WaitGroup
	for i := 0; i < config.MaxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			processTileClipIdentityWorker(workerID, inputLayerCopies[workerID], methodLayerCopies[workerID], inputTableName, methodTableName, config.PrecisionConfig, tilesChan, resultsChan)
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

	// 收集并合并结果
	return collectTileIdentityResults(resultsChan, resultLayer, len(tiles), config.ProgressCallback)
}

// processTileClipIdentityWorker 瓦片裁剪Identity工作协程
func processTileClipIdentityWorker(workerID int, inputLayerCopy, methodLayerCopy *GDALLayer, inputTableName, methodTableName string, precisionConfig *GeometryPrecisionConfig, tilesChan <-chan *TileIdentityInfo, resultsChan chan<- *TileIdentityResult) {
	for tile := range tilesChan {
		startTime := time.Now()

		result := &TileIdentityResult{
			TileIndex: tile.Index,
			Features:  make([]C.OGRFeatureH, 0),
		}

		// 处理单个瓦片的裁剪Identity
		features, err := processSingleTileClipIdentity(inputLayerCopy, methodLayerCopy, tile, workerID, inputTableName, methodTableName, precisionConfig)
		if err != nil {
			result.Error = fmt.Errorf("工作协程 %d 处理瓦片 %d 失败: %v", workerID, tile.Index, err)
		} else {
			result.Features = features
		}

		result.ProcessTime = time.Since(startTime)
		resultsChan <- result
	}
}

// processSingleTileClipIdentity 处理单个瓦片的裁剪Identity
func processSingleTileClipIdentity(
	inputLayerCopy, methodLayerCopy *GDALLayer, tile *TileIdentityInfo, workerID int, inputTableName, methodTableName string, precisionConfig *GeometryPrecisionConfig) (
	[]C.OGRFeatureH, error) {

	// 创建瓦片裁剪后的图层
	inputLayerName := C.CString(fmt.Sprintf("clipped_input_worker%d_tile%d", workerID, tile.Index))
	methodLayerName := C.CString(fmt.Sprintf("clipped_method_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(inputLayerName))
	defer C.free(unsafe.Pointer(methodLayerName))

	// 裁剪两个图层到当前瓦片范围
	inputTableNameC := C.CString(inputTableName)
	methodTableNameC := C.CString(methodTableName)
	defer C.free(unsafe.Pointer(inputTableNameC))
	defer C.free(unsafe.Pointer(methodTableNameC))

	clippedInputLayerPtr := C.clipLayerToTile(inputLayerCopy.layer,
		C.double(tile.MinX), C.double(tile.MinY),
		C.double(tile.MaxX), C.double(tile.MaxY), inputLayerName, inputTableNameC)

	clippedMethodLayerPtr := C.clipLayerToTile(methodLayerCopy.layer,
		C.double(tile.MinX), C.double(tile.MinY),
		C.double(tile.MaxX), C.double(tile.MaxY), methodLayerName, methodTableNameC)

	if clippedInputLayerPtr == nil || clippedMethodLayerPtr == nil {
		if clippedInputLayerPtr != nil {
			C.OGR_L_Dereference(clippedInputLayerPtr)
		}
		if clippedMethodLayerPtr != nil {
			C.OGR_L_Dereference(clippedMethodLayerPtr)
		}
		return nil, fmt.Errorf("裁剪图层到瓦片失败")
	}
	defer C.OGR_L_Dereference(clippedInputLayerPtr)
	defer C.OGR_L_Dereference(clippedMethodLayerPtr)

	clippedInputLayer := &GDALLayer{layer: clippedInputLayerPtr}
	clippedMethodLayer := &GDALLayer{layer: clippedMethodLayerPtr}

	// 检查裁剪后的图层是否有要素
	inputCount := clippedInputLayer.GetFeatureCount()

	if inputCount == 0 {
		return []C.OGRFeatureH{}, nil
	}

	// 创建结果临时图层
	resultTempName := C.CString(fmt.Sprintf("identity_result_worker%d_tile%d", workerID, tile.Index))
	defer C.free(unsafe.Pointer(resultTempName))

	srs := clippedInputLayer.GetSpatialRef()
	if srs == nil {
		srs = clippedMethodLayer.GetSpatialRef()
	}

	resultTempPtr := C.createMemoryLayer(resultTempName, C.wkbMultiPolygon, srs)
	if resultTempPtr == nil {
		return nil, fmt.Errorf("创建结果临时图层失败")
	}
	defer C.OGR_L_Dereference(resultTempPtr)

	resultTemp := &GDALLayer{layer: resultTempPtr}

	// 添加字段定义
	err := addIdentityFields(resultTemp, clippedInputLayer, clippedMethodLayer, inputTableName, methodTableName)
	if err != nil {
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	// 执行Identity分析
	err = executeIdentityAnalysis(clippedInputLayer, clippedMethodLayer, resultTemp, inputTableName, methodTableName, nil)
	if err != nil {
		return nil, fmt.Errorf("瓦片Identity分析失败: %v", err)
	}

	// 收集结果要素
	features := make([]C.OGRFeatureH, 0)
	resultTemp.ResetReading()

	resultTemp.IterateFeatures(func(feature C.OGRFeatureH) {
		clonedFeature := C.OGR_F_Clone(feature)
		if clonedFeature != nil {
			features = append(features, clonedFeature)
		}
	})

	return features, nil
}

// collectTileIdentityResults 收集瓦片Identity结果
func collectTileIdentityResults(resultsChan <-chan *TileIdentityResult, resultLayer *GDALLayer, totalTiles int, progressCallback ProgressCallback) error {
	processedTiles := 0
	var totalProcessTime time.Duration
	totalFeatures := 0

	resultMutex := sync.Mutex{}

	// 收集所有结果
	for result := range resultsChan {
		if result.Error != nil {
			// 清理资源
			for _, feature := range result.Features {
				if feature != nil {
					C.OGR_F_Destroy(feature)
				}
			}
			return result.Error
		}

		resultMutex.Lock()

		// 直接添加所有要素
		for _, feature := range result.Features {
			if feature != nil {
				C.OGR_L_CreateFeature(resultLayer.layer, feature)
				C.OGR_F_Destroy(feature)
			}
		}

		resultMutex.Unlock()

		processedTiles++
		totalProcessTime += result.ProcessTime
		totalFeatures += len(result.Features)

		// 更新进度
		if progressCallback != nil {
			progress := float64(processedTiles) / float64(totalTiles)
			message := fmt.Sprintf("已完成瓦片 %d/%d，累计生成要素 %d 个",
				processedTiles, totalTiles, totalFeatures)

			if !progressCallback(progress, message) {
				return fmt.Errorf("操作被用户取消")
			}
		}
	}

	fmt.Printf("瓦片裁剪Identity结果汇总完成: 处理 %d 个瓦片，生成 %d 个要素\n",
		processedTiles, totalFeatures)
	fmt.Printf("总处理时间: %v，平均每瓦片: %v\n",
		totalProcessTime, totalProcessTime/time.Duration(totalTiles))

	return nil
}

// createIdentityAnalysisResultLayer 创建Identity结果图层
func createIdentityAnalysisResultLayer(inputLayer, methodLayer *GDALLayer, inputTableName, methodTableName string) (*GDALLayer, error) {
	layerName := C.CString("identity_result")
	defer C.free(unsafe.Pointer(layerName))

	// 获取空间参考系统
	srs := inputLayer.GetSpatialRef()
	if srs == nil {
		srs = methodLayer.GetSpatialRef()
	}

	// 创建结果图层
	resultLayerPtr := C.createMemoryLayer(layerName, C.wkbMultiPolygon, srs)
	if resultLayerPtr == nil {
		return nil, fmt.Errorf("创建结果图层失败")
	}

	resultLayer := &GDALLayer{layer: resultLayerPtr}
	runtime.SetFinalizer(resultLayer, (*GDALLayer).cleanup)

	// 添加字段定义
	err := addIdentityFields(resultLayer, inputLayer, methodLayer, inputTableName, methodTableName)
	if err != nil {
		resultLayer.Close()
		return nil, fmt.Errorf("添加字段失败: %v", err)
	}

	return resultLayer, nil
}

// IdentitySourceIdentifierField Identity来源标识字段名
const IdentitySourceIdentifierField = "source_layer"

// addIdentityFields 添加Identity分析的字段
func addIdentityFields(resultLayer, inputLayer, methodLayer *GDALLayer, inputTableName, methodTableName string) error {
	// 首先添加来源标识字段
	sourceFieldName := C.CString(IdentitySourceIdentifierField)
	sourceFieldDefn := C.OGR_Fld_Create(sourceFieldName, C.OFTString)
	C.OGR_Fld_SetWidth(sourceFieldDefn, 50)
	err := C.OGR_L_CreateField(resultLayer.layer, sourceFieldDefn, 1)
	C.OGR_Fld_Destroy(sourceFieldDefn)
	C.free(unsafe.Pointer(sourceFieldName))

	if err != C.OGRERR_NONE {
		return fmt.Errorf("创建来源标识字段失败，错误代码: %d", int(err))
	}

	// 添加输入图层的字段（不使用前缀）
	err1 := addLayerFieldsWithoutPrefix(resultLayer, inputLayer)
	if err1 != nil {
		return fmt.Errorf("添加输入图层字段失败: %v", err1)
	}

	// 添加方法图层的字段（不使用前缀，处理重名字段）
	err2 := addLayerFieldsWithoutPrefix(resultLayer, methodLayer)
	if err2 != nil {
		return fmt.Errorf("添加方法图层字段失败: %v", err2)
	}

	return nil
}

// executeIdentityAnalysis 执行Identity分析
func executeIdentityAnalysis(inputLayer, methodLayer, resultLayer *GDALLayer, inputTableName, methodTableName string, progressCallback ProgressCallback) error {
	// 设置GDAL选项
	var options **C.char
	defer func() {
		if options != nil {
			C.CSLDestroy(options)
		}
	}()

	skipFailuresOpt := C.CString("SKIP_FAILURES=YES")
	promoteToMultiOpt := C.CString("PROMOTE_TO_MULTI=YES")
	keepLowerDimOpt := C.CString("KEEP_LOWER_DIMENSION_GEOMETRIES=NO")
	defer C.free(unsafe.Pointer(skipFailuresOpt))
	defer C.free(unsafe.Pointer(promoteToMultiOpt))
	defer C.free(unsafe.Pointer(keepLowerDimOpt))

	options = C.CSLAddString(options, skipFailuresOpt)
	options = C.CSLAddString(options, promoteToMultiOpt)
	options = C.CSLAddString(options, keepLowerDimOpt)

	// 执行Identity操作
	return executeGDALIdentityWithProgress(inputLayer, methodLayer, resultLayer, options, progressCallback)
}

// executeGDALIdentityWithProgress 执行带进度的GDAL Identity操作
func executeGDALIdentityWithProgress(inputLayer, methodLayer, resultLayer *GDALLayer, options **C.char, progressCallback ProgressCallback) error {
	var progressData *ProgressData
	var progressArg unsafe.Pointer

	// 设置进度回调
	if progressCallback != nil {
		progressData = &ProgressData{
			callback:  progressCallback,
			cancelled: false,
		}
		progressArg = unsafe.Pointer(uintptr(unsafe.Pointer(progressData)))

		progressDataMutex.Lock()
		progressDataMap[uintptr(progressArg)] = progressData
		progressDataMutex.Unlock()

		defer func() {
			progressDataMutex.Lock()
			delete(progressDataMap, uintptr(progressArg))
			progressDataMutex.Unlock()
		}()
	}

	// 调用GDAL的Identity函数
	var err C.OGRErr
	if progressCallback != nil {
		err = C.performIdentityWithProgress(inputLayer.layer, methodLayer.layer, resultLayer.layer, options, progressArg)
	} else {
		err = C.OGR_L_Identity(inputLayer.layer, methodLayer.layer, resultLayer.layer, options, nil, nil)
	}

	if err != C.OGRERR_NONE {
		return fmt.Errorf("GDAL Identity操作失败，错误代码: %d", int(err))
	}

	return nil
}

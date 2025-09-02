/*
Copyright (C) 2024 [Your Name]

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

import (
	"fmt"
	"path/filepath"
	"runtime"
	"time"
)

func performSpatialIntersectionTest(shpFile1, shpFile2, outputFile string) error {
	startTime := time.Now()

	// 1. 读取第一个shapefile
	fmt.Println("正在读取第一个shapefile...")
	reader1, err := NewFileGeoReader(shpFile1)
	if err != nil {
		return fmt.Errorf("创建第一个文件读取器失败: %v", err)
	}

	layer1, err := reader1.ReadShapeFile()
	if err != nil {
		return fmt.Errorf("读取第一个shapefile失败: %v", err)
	}

	// 打印第一个图层信息
	fmt.Println("第一个图层信息:")
	layer1.PrintLayerInfo()

	// 2. 读取第二个shapefile
	fmt.Println("\n正在读取第二个shapefile...")
	reader2, err := NewFileGeoReader(shpFile2)
	if err != nil {
		layer1.Close()
		return fmt.Errorf("创建第二个文件读取器失败: %v", err)
	}

	layer2, err := reader2.ReadShapeFile()
	if err != nil {
		layer1.Close()
		return fmt.Errorf("读取第二个shapefile失败: %v", err)
	}

	// 打印第二个图层信息
	fmt.Println("第二个图层信息:")
	layer2.PrintLayerInfo()

	// 3. 配置并行相交分析参数
	config := &ParallelGeosConfig{
		TileCount:        4,                // 4x4分块
		MaxWorkers:       runtime.NumCPU(), // 使用所有CPU核心
		BufferDistance:   0.0001,           // 分块缓冲距离
		IsMergeTile:      true,             // 合并分块结果
		ProgressCallback: progressCallback, // 进度回调函数
		PrecisionConfig: &GeometryPrecisionConfig{
			Enabled:       true,
			GridSize:      0.000000001, // 几何精度网格大小
			PreserveTopo:  true,        // 保持拓扑
			KeepCollapsed: false,       // 不保留退化几何
		},
	}

	// 4. 选择字段合并策略
	strategy := MergeWithPrefix // 使用前缀区分字段来源

	fmt.Printf("\n开始执行空间相交分析...")
	fmt.Printf("分块配置: %dx%d, 工作线程: %d\n",
		config.TileCount, config.TileCount, config.MaxWorkers)
	fmt.Printf("字段合并策略: %s\n", strategy.String())

	// 5. 执行空间相交分析
	result, err := SpatialIntersectionAnalysis(layer1, layer2, strategy, config)
	if err != nil {
		return fmt.Errorf("空间相交分析执行失败: %v", err)
	}

	analysisTime := time.Since(startTime)
	fmt.Printf("\n相交分析完成! 耗时: %v\n", analysisTime)
	fmt.Printf("结果要素数量: %d\n", result.ResultCount)

	// 6. 将结果写出为shapefile
	fmt.Println("正在写出结果到shapefile...")
	writeStartTime := time.Now()

	// 获取输出文件的图层名称（不含扩展名）
	layerName := getFileNameWithoutExt(outputFile)

	err = WriteShapeFileLayer(result.OutputLayer, outputFile, layerName, true)
	if err != nil {
		result.OutputLayer.Close()
		return fmt.Errorf("写出shapefile失败: %v", err)
	}

	writeTime := time.Since(writeStartTime)
	totalTime := time.Since(startTime)

	fmt.Printf("结果写出完成! 耗时: %v\n", writeTime)
	fmt.Printf("总耗时: %v\n", totalTime)
	fmt.Printf("输出文件: %s\n", outputFile)

	// 7. 验证输出文件
	err = verifyOutputFile(outputFile)
	if err != nil {
		fmt.Printf("警告: 输出文件验证失败: %v\n", err)
	} else {
		fmt.Println("输出文件验证成功!")
	}

	// 清理资源
	result.OutputLayer.Close()

	return nil
}

// progressCallback 进度回调函数
func progressCallback(complete float64, message string) bool {
	// 显示进度信息
	fmt.Printf("\r进度: %.1f%% - %s", complete*100, message)

	// 如果进度完成，换行
	if complete >= 1.0 {
		fmt.Println()
	}

	// 返回true继续执行，返回false取消执行
	return true
}

// getFileNameWithoutExt 获取不含扩展名的文件名
func getFileNameWithoutExt(filePath string) string {
	fileName := filepath.Base(filePath)
	return fileName[:len(fileName)-len(filepath.Ext(fileName))]
}

// verifyOutputFile 验证输出文件
func verifyOutputFile(filePath string) error {
	// 读取输出文件验证
	reader, err := NewFileGeoReader(filePath)
	if err != nil {
		return fmt.Errorf("无法读取输出文件: %v", err)
	}

	layer, err := reader.ReadShapeFile()
	if err != nil {
		return fmt.Errorf("无法读取输出图层: %v", err)
	}
	defer layer.Close()

	// 打印输出图层信息
	fmt.Println("\n输出图层信息:")
	layer.PrintLayerInfo()

	// 检查要素数量
	featureCount := layer.GetFeatureCount()
	if featureCount == 0 {
		return fmt.Errorf("输出文件中没有要素")
	}

	fmt.Printf("验证通过: 输出文件包含 %d 个要素\n", featureCount)
	return nil
}

// 高级测试函数：测试不同的字段合并策略
func testDifferentStrategies(shpFile1, shpFile2 string) error {
	strategies := []FieldMergeStrategy{
		UseTable1Fields,
		UseTable2Fields,
		MergePreferTable1,
		MergePreferTable2,
		MergeWithPrefix,
	}

	config := &ParallelGeosConfig{
		TileCount:        2,
		MaxWorkers:       runtime.NumCPU(),
		BufferDistance:   0.001,
		IsMergeTile:      true,
		ProgressCallback: progressCallback,
	}

	for i, strategy := range strategies {
		fmt.Printf("\n=== 测试策略 %d: %s ===\n", i+1, strategy.String())

		outputFile := fmt.Sprintf("output/test_strategy_%d.shp", i+1)

		// 读取图层
		layer1, err := ReadShapeFileLayer(shpFile1)
		if err != nil {
			return err
		}

		layer2, err := ReadShapeFileLayer(shpFile2)
		if err != nil {
			layer1.Close()
			return err
		}

		// 执行分析
		result, err := SpatialIntersectionAnalysis(layer1, layer2, strategy, config)
		if err != nil {
			return fmt.Errorf("策略 %s 执行失败: %v", strategy.String(), err)
		}

		// 写出结果
		layerName := fmt.Sprintf("strategy_%d", i+1)
		err = WriteShapeFileLayer(result.OutputLayer, outputFile, layerName, true)
		if err != nil {
			result.OutputLayer.Close()
			return fmt.Errorf("策略 %s 写出失败: %v", strategy.String(), err)
		}

		fmt.Printf("策略 %s 完成，结果要素: %d，输出: %s\n",
			strategy.String(), result.ResultCount, outputFile)

		result.OutputLayer.Close()
	}

	return nil
}

// 性能测试函数
func performanceTest(shpFile1, shpFile2 string) error {
	fmt.Println("\n=== 性能测试 ===")

	// 测试不同的分块配置
	tileConfigs := []int{2, 4, 8}

	for _, tileCount := range tileConfigs {
		fmt.Printf("\n--- 测试分块配置: %dx%d ---\n", tileCount, tileCount)

		config := &ParallelGeosConfig{
			TileCount:        tileCount,
			MaxWorkers:       runtime.NumCPU(),
			BufferDistance:   0.001,
			IsMergeTile:      true,
			ProgressCallback: nil, // 性能测试时不显示进度
		}

		startTime := time.Now()

		// 读取图层
		layer1, err := ReadShapeFileLayer(shpFile1)
		if err != nil {
			return err
		}

		layer2, err := ReadShapeFileLayer(shpFile2)
		if err != nil {
			layer1.Close()
			return err
		}

		// 执行分析
		result, err := SpatialIntersectionAnalysis(layer1, layer2,
			MergePreferTable1, config)
		if err != nil {
			return err
		}

		duration := time.Since(startTime)
		fmt.Printf("分块 %dx%d: 耗时 %v, 结果要素 %d\n",
			tileCount, tileCount, duration, result.ResultCount)

		result.OutputLayer.Close()
	}

	return nil
}

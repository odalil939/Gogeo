# Gogeo - 高性能Go语言GIS空间分析库

[![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.18-blue.svg)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![GDAL](https://img.shields.io/badge/GDAL-%3E%3D%203.0-orange.svg)](https://gdal.org/)

Gogeo是一个基于GDAL/OGR的高性能Go语言GIS空间分析库，专为大规模地理数据处理而设计。通过并行计算、瓦片分块和精度控制等技术，提供了完整的空间分析功能集。

## ✨ 主要特性

### 🚀 高性能并行计算
- **瓦片分块处理**：自动将大数据集分割为小块并行处理
- **多线程工作池**：可配置的并发工作线程数
- **内存优化**：智能的内存管理和资源清理机制
- **进度监控**：实时进度回调和用户取消支持

### 🎯 完整的空间分析功能
- **Clip（裁剪）**：用一个图层裁剪另一个图层
- **Erase（擦除）**：从输入图层中移除重叠部分
- **Identity（叠加）**：保留输入要素并添加重叠属性
- **Intersect（相交）**：计算两个图层的交集
- **SymDifference（对称差）**：计算两个图层的对称差集
- **Union（联合）**：计算两个图层的并集
- **Update（更新）**：用一个图层更新另一个图层

### 🔧 高级功能
- **几何精度控制**：可配置的几何精度网格
- **字段管理**：智能的字段映射和冲突处理
- **数据格式支持**：支持Shapefile、GeoJSON、PostGIS等多种格式
- **空间索引**：自动空间索引优化查询性能
- **边界处理**：智能的边界要素去重机制

## 📦 安装

### 前置条件
确保系统已安装GDAL开发库：

**Ubuntu/Debian:**
```bash
sudo apt-get update
sudo apt-get install libgdal-dev gdal-bin
```

**CentOS/RHEL:**
```bash
sudo yum install gdal-devel gdal
```

**macOS:**
```bash
brew install gdal
```

**Windows:**
下载并安装 [OSGeo4W](https://trac.osgeo.org/osgeo4w/) 或 [GDAL Windows binaries](https://www.gisinternals.com/)

### 安装Gogeo
```bash
go get github.com/yourusername/gogeo
```

## 🚀 快速开始

### 基本用法示例

```go
package main

import (
    "fmt"
    "log"
    "github.com/yourusername/gogeo"
)

func main() {
    // 初始化GDAL
    gogeo.RegisterAllDrivers()
    defer gogeo.Cleanup()

    // 读取输入数据
    inputLayer, err := gogeo.OpenLayer("input.shp", 0)
    if err != nil {
        log.Fatal("打开输入图层失败:", err)
    }
    defer inputLayer.Close()

    clipLayer, err := gogeo.OpenLayer("clip.shp", 0)
    if err != nil {
        log.Fatal("打开裁剪图层失败:", err)
    }
    defer clipLayer.Close()

    // 配置并行处理参数
    config := &gogeo.ParallelGeosConfig{
        MaxWorkers: 8,           // 8个并发线程
        TileCount:  4,           // 4x4瓦片分块
        IsMergeTile: true,       // 启用结果融合
        PrecisionConfig: &gogeo.GeometryPrecisionConfig{
            Enabled:   true,
            GridSize:  0.001,    // 1mm精度
        },
        ProgressCallback: func(progress float64, message string) bool {
            fmt.Printf("进度: %.1f%% - %s\n", progress*100, message)
            return true // 返回false可取消操作
        },
    }

    // 执行空间裁剪分析
    result, err := gogeo.SpatialClipAnalysis(inputLayer, clipLayer, config)
    if err != nil {
        log.Fatal("空间分析失败:", err)
    }
    defer result.OutputLayer.Close()

    // 保存结果
    err = gogeo.SaveLayer(result.OutputLayer, "output.shp", "ESRI Shapefile")
    if err != nil {
        log.Fatal("保存结果失败:", err)
    }

    fmt.Printf("分析完成！生成了 %d 个要素\n", result.ResultCount)
}
```

### 高级配置示例

```go
// 自定义精度配置
precisionConfig := &gogeo.GeometryPrecisionConfig{
    Enabled:              true,
    GridSize:             0.0001,  // 0.1mm精度
    PreserveCollinear:    true,    // 保留共线点
    KeepCollapsed:        false,   // 移除退化几何
}

// 高性能配置
config := &gogeo.ParallelGeosConfig{
    MaxWorkers:      16,           // 16线程并行
    TileCount:       8,            // 8x8=64个瓦片
    IsMergeTile:     true,
    PrecisionConfig: precisionConfig,
    ProgressCallback: customProgressHandler,
}

// 执行不同类型的空间分析
clipResult, _ := gogeo.SpatialClipAnalysis(layer1, layer2, config)
eraseResult, _ := gogeo.SpatialEraseAnalysis(layer1, layer2, config)
identityResult, _ := gogeo.SpatialIdentityAnalysis(layer1, layer2, config)
intersectResult, _ := gogeo.SpatialIntersectAnalysis(layer1, layer2, config)
unionResult, _ := gogeo.SpatialUnionAnalysis(layer1, layer2, config)
symDiffResult, _ := gogeo.SpatialSymDifferenceAnalysis(layer1, layer2, config)
updateResult, _ := gogeo.SpatialUpdateAnalysis(layer1, layer2, config)
```

## 📚 API文档

### 核心数据结构

```go
// 并行处理配置
type ParallelGeosConfig struct {
    MaxWorkers       int                        // 最大工作线程数
    TileCount        int                        // 瓦片分块数量(N×N)
    IsMergeTile      bool                       // 是否融合瓦片结果
    PrecisionConfig  *GeometryPrecisionConfig   // 几何精度配置
    ProgressCallback ProgressCallback           // 进度回调函数
}

// 几何精度配置
type GeometryPrecisionConfig struct {
    Enabled           bool    // 是否启用精度控制
    GridSize          float64 // 精度网格大小
    PreserveCollinear bool    // 保留共线点
    KeepCollapsed     bool    // 保留退化几何
}

// 分析结果
type GeosAnalysisResult struct {
    OutputLayer *GDALLayer // 输出图层
    ResultCount int        // 结果要素数量
}
```

### 主要函数

#### 空间分析函数
```go
// 空间裁剪
func SpatialClipAnalysis(inputLayer, clipLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// 空间擦除
func SpatialEraseAnalysis(inputLayer, eraseLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// 空间叠加
func SpatialIdentityAnalysis(inputLayer, methodLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// 空间相交
func SpatialIntersectAnalysis(inputLayer, intersectLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// 空间联合
func SpatialUnionAnalysis(inputLayer, unionLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// 对称差集
func SpatialSymDifferenceAnalysis(inputLayer, diffLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// 空间更新
func SpatialUpdateAnalysis(inputLayer, updateLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)
```

#### 数据I/O函数
```go
// 打开图层
func OpenLayer(filename string, layerIndex int) (*GDALLayer, error)

// 保存图层
func SaveLayer(layer *GDALLayer, filename string, driverName string) error

// 创建图层
func CreateLayer(filename string, driverName string, geomType OGRwkbGeometryType, srs *OGRSpatialReference) (*GDALLayer, error)
```

## 🎯 使用场景

### 1. 大规模土地利用分析
```go
// 处理省级土地利用数据与行政边界的叠加分析
landUseResult, err := gogeo.SpatialIdentityAnalysis(landUseLayer, adminBoundaryLayer, &gogeo.ParallelGeosConfig{
    MaxWorkers:  12,
    TileCount:   6,
    IsMergeTile: true,
})
```

### 2. 环境影响评估
```go
// 计算项目影响区域与保护区的交集
impactResult, err := gogeo.SpatialIntersectAnalysis(projectAreaLayer, protectedAreaLayer, config)
```

### 3. 城市规划分析
```go
// 从建设用地中擦除生态保护区
buildableResult, err := gogeo.SpatialEraseAnalysis(constructionLayer, ecologyLayer, config)
```

## ⚡ 性能优化

### 1. 并行配置建议
```go
// CPU密集型任务
config.MaxWorkers = runtime.NumCPU()

// I/O密集型任务  
config.MaxWorkers = runtime.NumCPU() * 2

// 大数据集处理
config.TileCount = 8  // 64个瓦片
```

### 2. 内存优化
```go
// 启用结果融合以减少内存占用
config.IsMergeTile = true

// 适当的精度设置避免过度计算
config.PrecisionConfig.GridSize = 0.001  // 1mm精度通常足够
```

### 3. 数据预处理
- 建议在分析前对数据建立空间索引
- 移除无效几何体
- 统一坐标参考系统

## 🔧 配置参数详解

### MaxWorkers（工作线程数）
- **推荐值**：CPU核心数的1-2倍
- **影响**：过多会导致上下文切换开销，过少无法充分利用CPU

### TileCount（瓦片数量）
- **推荐值**：4-8（生成16-64个瓦片）
- **影响**：瓦片过多会增加边界处理开销，过少无法有效并行

### GridSize（精度网格）
- **推荐值**：0.001-0.0001（1mm-0.1mm）
- **影响**：过大会丢失细节，过小会增加计算开销

## 🐛 故障排除

### 常见问题

1. **GDAL库未找到**
   ```
   错误：cannot find GDAL library
   解决：确保GDAL开发库已正确安装并设置环境变量
   ```

2. **内存不足**
   ```
   错误：out of memory
   解决：减少MaxWorkers或增加TileCount进行更细粒度分块
   ```

3. **几何错误**
   ```
   错误：invalid geometry
   解决：启用精度控制或预处理数据移除无效几何
   ```

### 调试技巧
```go
// 启用详细日志
config.ProgressCallback = func(progress float64, message string) bool {
    log.Printf("Progress: %.2f%% - %s", progress*100, message)
    return true
}

// 检查数据有效性
if layer.GetFeatureCount() == 0 {
    log.Println("警告：图层为空")
}
```

## 🤝 贡献指南

我们欢迎各种形式的贡献！

### 如何贡献
1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 开启 Pull Request

### 开发环境设置
```bash
# 克隆仓库
git clone https://github.com/yourusername/gogeo.git
cd gogeo

# 安装依赖
go mod tidy

# 运行测试
go test ./...

# 构建示例
go build ./examples/...
```

## 📄 许可证

本项目采用 MIT 许可证 - 查看 [LICENSE](LICENSE) 文件了解详情。

## 🙏 致谢

- [GDAL/OGR](https://gdal.org/) - 强大的地理空间数据处理库
- [GEOS](https://trac.osgeo.org/geos/) - 几何计算引擎
- Go社区的所有贡献者

## 📞 联系方式

- 项目主页：https://github.com/yourusername/gogeo
- 问题反馈：https://github.com/yourusername/gogeo/issues
- 邮箱：your.email@example.com

---

⭐ 如果这个项目对你有帮助，请给我们一个星标！

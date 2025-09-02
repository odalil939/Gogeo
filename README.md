# Gogeo - High-Performance GIS Spatial Analysis Library for Go

[![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.18-blue.svg)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![GDAL](https://img.shields.io/badge/GDAL-%3E%3D%203.0-orange.svg)](https://gdal.org/)

Gogeo is a high-performance Go GIS spatial analysis library built on GDAL/OGR, designed for large-scale geospatial data processing. It provides comprehensive spatial analysis capabilities through parallel computing, tile-based processing, and precision control.

## ✨ Key Features

### 🚀 High-Performance Parallel Computing
- **Tile-based Processing**: Automatically splits large datasets into tiles for parallel processing
- **Multi-threaded Worker Pool**: Configurable concurrent worker threads
- **Memory Optimization**: Smart memory management and resource cleanup
- **Progress Monitoring**: Real-time progress callbacks and user cancellation support

### 🎯 Complete Spatial Analysis Operations
- **Clip**: Clip one layer with another layer
- **Erase**: Remove overlapping parts from input layer
- **Identity**: Preserve input features and add overlapping attributes
- **Intersect**: Calculate intersection of two layers
- **SymDifference**: Calculate symmetric difference of two layers
- **Union**: Calculate union of two layers
- **Update**: Update one layer with another layer

### 📁 Comprehensive Data I/O Support
- **PostGIS Database**: Read from and write to PostGIS databases
- **Shapefile**: Support for ESRI Shapefile format
- **File Geodatabase**: Support for ESRI File Geodatabase (.gdb)
- **Format Conversion**: Convert between different geospatial formats
- **Layer Management**: List layers, get layer information, and metadata

### 🔧 Advanced Features
- **Geometry Precision Control**: Configurable geometry precision grid
- **Field Management**: Smart field mapping and conflict resolution
- **Spatial Indexing**: Automatic spatial index optimization for query performance
- **Boundary Processing**: Intelligent boundary feature deduplication
- **Resource Management**: Automatic cleanup with finalizers

## 📦 Installation

### Prerequisites
Ensure GDAL development libraries are installed on your system:

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
Download and install [OSGeo4W](https://trac.osgeo.org/osgeo4w/) or [GDAL Windows binaries](https://www.gisinternals.com/)

### Install Gogeo
```bash
go get github.com/yourusername/gogeo
```

## 🚀 Quick Start

### Basic Usage Example

```go
package main

import (
    "fmt"
    "log"
    "github.com/yourusername/gogeo"
)

func main() {
    // Initialize GDAL
    gogeo.RegisterAllDrivers()
    defer gogeo.Cleanup()

    // Read input data from Shapefile
    inputLayer, err := gogeo.ReadShapeFileLayer("input.shp")
    if err != nil {
        log.Fatal("Failed to open input layer:", err)
    }
    defer inputLayer.Close()

    clipLayer, err := gogeo.ReadShapeFileLayer("clip.shp")
    if err != nil {
        log.Fatal("Failed to open clip layer:", err)
    }
    defer clipLayer.Close()

    // Configure parallel processing parameters
    config := &gogeo.ParallelGeosConfig{
        MaxWorkers: 8,           // 8 concurrent threads
        TileCount:  4,           // 4x4 tile grid
        IsMergeTile: true,       // Enable result merging
        PrecisionConfig: &gogeo.GeometryPrecisionConfig{
            Enabled:   true,
            GridSize:  0.001,    // 1mm precision
        },
        ProgressCallback: func(progress float64, message string) bool {
            fmt.Printf("Progress: %.1f%% - %s\n", progress*100, message)
            return true // Return false to cancel operation
        },
    }

    // Execute spatial clip analysis
    result, err := gogeo.SpatialClipAnalysis(inputLayer, clipLayer, config)
    if err != nil {
        log.Fatal("Spatial analysis failed:", err)
    }
    defer result.OutputLayer.Close()

    // Save result to Shapefile
    err = gogeo.WriteShapeFileLayer(result.OutputLayer, "output.shp", "result", true)
    if err != nil {
        log.Fatal("Failed to save result:", err)
    }

    fmt.Printf("Analysis completed! Generated %d features\n", result.ResultCount)
}
```

### PostGIS Database Example

```go
// Configure PostGIS connection
config := &gogeo.PostGISConfig{
    Host:     "localhost",
    Port:     "5432",
    Database: "gis_db",
    User:     "postgres",
    Password: "password",
    Schema:   "public",
    Table:    "land_use",
}

// Create PostGIS reader
reader := gogeo.NewPostGISReader(config)
layer, err := reader.ReadGeometryTable()
if err != nil {
    log.Fatal("Failed to read PostGIS table:", err)
}
defer layer.Close()

// Print layer information
layer.PrintLayerInfo()
```

### File Format Conversion Example

```go
// Convert Shapefile to File Geodatabase
err := gogeo.ConvertFile(
    "input.shp",           // Source file
    "output.gdb",          // Target file
    "",                    // Source layer name (empty for first layer)
    "converted_layer",     // Target layer name
    true,                  // Overwrite if exists
)
if err != nil {
    log.Fatal("Conversion failed:", err)
}
```

## 📚 API Documentation

### Core Data Structures

```go
// Parallel processing configuration
type ParallelGeosConfig struct {
    MaxWorkers       int                        // Maximum worker threads
    TileCount        int                        // Tile count (N×N grid)
    IsMergeTile      bool                       // Whether to merge tile results
    PrecisionConfig  *GeometryPrecisionConfig   // Geometry precision configuration
    ProgressCallback ProgressCallback           // Progress callback function
}

// Geometry precision configuration
type GeometryPrecisionConfig struct {
    Enabled           bool    // Enable precision control
    GridSize          float64 // Precision grid size
    PreserveCollinear bool    // Preserve collinear points
    KeepCollapsed     bool    // Keep collapsed geometries
}

// Analysis result
type GeosAnalysisResult struct {
    OutputLayer *GDALLayer // Output layer
    ResultCount int        // Number of result features
}

// PostGIS connection configuration
type PostGISConfig struct {
    Host     string // Database host
    Port     string // Database port
    Database string // Database name
    User     string // Username
    Password string // Password
    Schema   string // Schema name
    Table    string // Table name
}
```

### Spatial Analysis Functions

```go
// Spatial clip
func SpatialClipAnalysis(inputLayer, clipLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// Spatial erase
func SpatialEraseAnalysis(inputLayer, eraseLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// Spatial identity
func SpatialIdentityAnalysis(inputLayer, methodLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// Spatial intersect
func SpatialIntersectAnalysis(inputLayer, intersectLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// Spatial union
func SpatialUnionAnalysis(inputLayer, unionLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// Symmetric difference
func SpatialSymDifferenceAnalysis(inputLayer, diffLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)

// Spatial update
func SpatialUpdateAnalysis(inputLayer, updateLayer *GDALLayer, config *ParallelGeosConfig) (*GeosAnalysisResult, error)
```

### Data I/O Functions

```go
// Read functions
func ReadShapeFileLayer(filePath string, layerName ...string) (*GDALLayer, error)
func ReadGDBLayer(filePath string, layerName ...string) (*GDALLayer, error)
func ReadGeospatialFile(filePath string, layerName ...string) (*GDALLayer, error)

// Write functions
func WriteShapeFileLayer(sourceLayer *GDALLayer, filePath string, layerName string, overwrite bool) error
func WriteGDBLayer(sourceLayer *GDALLayer, filePath string, layerName string, overwrite bool) error
func WriteGeospatialFile(sourceLayer *GDALLayer, filePath string, layerName string, overwrite bool) error

// Utility functions
func ConvertFile(sourceFilePath, targetFilePath, sourceLayerName, targetLayerName string, overwrite bool) error
func CopyLayerToFile(sourceLayer *GDALLayer, targetFilePath, targetLayerName string, overwrite bool) error
```

### PostGIS Functions

```go
// Create PostGIS reader
func NewPostGISReader(config *PostGISConfig) *PostGISReader

// Read geometry table
func (r *PostGISReader) ReadGeometryTable() (*GDALLayer, error)

// Convenience function
func MakePGReader(table string) *PostGISReader
```

## 🎯 Use Cases

### 1. Large-Scale Land Use Analysis
```go
// Process provincial land use data with administrative boundaries
landUseResult, err := gogeo.SpatialIdentityAnalysis(landUseLayer, adminBoundaryLayer, &gogeo.ParallelGeosConfig{
    MaxWorkers:  12,
    TileCount:   6,
    IsMergeTile: true,
})
```

### 2. Environmental Impact Assessment
```go
// Calculate intersection of project impact area with protected areas
impactResult, err := gogeo.SpatialIntersectAnalysis(projectAreaLayer, protectedAreaLayer, config)
```

### 3. Urban Planning Analysis
```go
// Erase ecological protection areas from construction land
buildableResult, err := gogeo.SpatialEraseAnalysis(constructionLayer, ecologyLayer, config)
```

### 4. Data Format Migration
```go
// Migrate Shapefile data to PostGIS
sourceLayer, _ := gogeo.ReadShapeFileLayer("data.shp")
// Process and save to PostGIS (implementation depends on your PostGIS writer)
```

## ⚡ Performance Optimization

### 1. Parallel Configuration Recommendations
```go
// CPU-intensive tasks
config.MaxWorkers = runtime.NumCPU()

// I/O-intensive tasks  
config.MaxWorkers = runtime.NumCPU() * 2

// Large dataset processing
config.TileCount = 8  // 64 tiles
```

### 2. Memory Optimization
```go
// Enable result merging to reduce memory usage
config.IsMergeTile = true

// Appropriate precision settings to avoid over-computation
config.PrecisionConfig.GridSize = 0.001  // 1mm precision is usually sufficient
```

### 3. Data Preprocessing
- Build spatial indexes on data before analysis
- Remove invalid geometries
- Ensure consistent coordinate reference systems

## 🔧 Configuration Parameters

### MaxWorkers (Worker Thread Count)
- **Recommended**: 1-2 times CPU core count
- **Impact**: Too many causes context switching overhead, too few underutilizes CPU

### TileCount (Tile Count)
- **Recommended**: 4-8 (generates 16-64 tiles)
- **Impact**: Too many tiles increase boundary processing overhead, too few reduce parallelism

### GridSize (Precision Grid)
- **Recommended**: 0.001-0.0001 (1mm-0.1mm)
- **Impact**: Too large loses detail, too small increases computation overhead

## 🐛 Troubleshooting

### Common Issues

1. **GDAL Library Not Found**
   ```
   Error: cannot find GDAL library
   Solution: Ensure GDAL development libraries are properly installed and environment variables are set
   ```

2. **Out of Memory**
   ```
   Error: out of memory
   Solution: Reduce MaxWorkers or increase TileCount for finer granularity
   ```

3. **Invalid Geometry**
   ```
   Error: invalid geometry
   Solution: Enable precision control or preprocess data to remove invalid geometries
   ```

4. **PostGIS Connection Failed**
   ```
   Error: connection failed
   Solution: Check database connection parameters and ensure PostgreSQL/PostGIS is running
   ```

### Debugging Tips
```go
// Enable verbose logging
config.ProgressCallback = func(progress float64, message string) bool {
    log.Printf("Progress: %.2f%% - %s", progress*100, message)
    return true
}

// Check data validity
if layer.GetFeatureCount() == 0 {
    log.Println("Warning: Layer is empty")
}

// Print layer information
layer.PrintLayerInfo()
```

## 🤝 Contributing

We welcome contributions of all kinds!

### How to Contribute
1. Fork the repository
2. Create a feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

### Development Environment Setup
```bash
# Clone repository
git clone https://github.com/yourusername/gogeo.git
cd gogeo

# Install dependencies
go mod tidy

# Run tests
go test ./...

# Build examples
go build ./examples/...
```

### Code Style
- Follow Go conventions and best practices
- Add comprehensive tests for new features
- Update documentation for API changes
- Ensure proper resource cleanup

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- [GDAL/OGR](https://gdal.org/) - Powerful geospatial data processing library
- [GEOS](https://trac.osgeo.org/geos/) - Geometry computation engine
- [PostGIS](https://postgis.net/) - Spatial database extension for PostgreSQL
- All contributors to the Go community

## 📞 Contact

- Project Homepage: https://github.com/yourusername/gogeo
- Issue Tracker: https://github.com/yourusername/gogeo/issues
- Email: 1131698384@qq.com

## 🔗 Related Projects

- [GDAL Go Bindings](https://github.com/lukeroth/gdal) - Alternative GDAL bindings for Go
- [PostGIS](https://postgis.net/) - Spatial database extension
- [GEOS](https://trac.osgeo.org/geos/) - Geometry engine

---

⭐ If this project helps you, please give us a star!

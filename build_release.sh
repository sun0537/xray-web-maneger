#!/bin/bash

# =============================================================================
# Xray Web Manager - 交叉编译和打包脚本
#
# 这个脚本会为 Windows 和 Linux (32/64位) 构建可执行文件,
# 并将它们与配置文件示例、README 一起打包。
#
# 用法:
#   1. 确保此脚本有可执行权限: chmod +x build_release.sh
#   2. 运行脚本并传入版本号 (例如 v1.0.0):
#      ./build_release.sh v1.0.0
#
# 依赖:
#   - go (Go 语言编译器)
#   - zip (用于创建 Windows 压缩包)
#   - tar (用于创建 Linux 压缩包)
# =============================================================================

# --- 脚本设置 ---

# (1) 如果任何命令失败，脚本将立即退出
set -e

# (2) 获取版本号
# 从脚本的第一个参数获取版本号 (例如: ./build_release.sh v1.0.0)
VERSION=$1
if [ -z "$VERSION" ]; then
  echo "错误: 未提供版本号。"
  echo "用法: ./build_release.sh <版本号> (例如: v1.0.0)"
  exit 1
fi

# (3) 定义变量
APP_NAME="xray-web-manager"
BUILD_DIR="build_temp"      # 临时的打包中转目录
RELEASE_DIR="release"     # 最终的 release 包输出目录

# (4) 定义要打包的额外文件 (请确保它们存在于根目录)
ASSETS="config.yaml README.md"

# (5) 定义编译目标 (格式: "GOOS/GOARCH")
TARGETS="linux/amd64 linux/386 windows/amd64 windows/386"

# --- 脚本开始 ---

echo "🚀 开始构建 $APP_NAME 版本 $VERSION ..."

# 清理并创建目录
rm -rf $BUILD_DIR $RELEASE_DIR
mkdir -p $BUILD_DIR $RELEASE_DIR

# 循环遍历所有编译目标
for target in $TARGETS; do
  # 拆分 GOOS 和 GOARCH
  GOOS=$(echo $target | cut -d'/' -f1)
  GOARCH=$(echo $target | cut -d'/' -f2)
  
  echo "-------------------------------------"
  echo "📦 正在构建: $GOOS / $GOARCH"

  # --- 1. 设置输出名称 ---
  EXE_NAME=$APP_NAME
  if [ "$GOOS" = "windows" ]; then
    EXE_NAME="$APP_NAME.exe"
  fi
  
  # 最终的包名 (例如: xray-web-manager-v1.0.0-linux-amd64)
  PACKAGE_NAME="${APP_NAME}-${VERSION}-${GOOS}-${GOARCH}"
  
  # 临时的中转目录 (例如: build_temp/xray-web-manager-v1.0.0-linux-amd64)
  STAGING_DIR="$BUILD_DIR/$PACKAGE_NAME"
  mkdir -p $STAGING_DIR

  # --- 2. 交叉编译 ---
  echo "  -> 正在编译..."
  # -ldflags="-s -w" (优化: 移除调试信息，减小体积)
  # -o (输出: 到我们临时的中转目录)
  GOOS=$GOOS GOARCH=$GOARCH go build -ldflags="-s -w" -o "$STAGING_DIR/$EXE_NAME" .
  
  if [ $? -ne 0 ]; then
      echo "  -> ❌ 编译失败: $GOOS/$GOARCH"
      exit 1
  fi

  # --- 3. 复制配套文件 ---
  echo "  -> 正在复制配套文件..."
  cp $ASSETS $STAGING_DIR/

  # --- 4. 打包 ---
  echo "  -> 正在打包..."
  
  if [ "$GOOS" = "windows" ]; then
    # --- Windows: 创建 .zip ---
    # (cd ... && zip ...) 是一个技巧:
    #   cd $BUILD_DIR       (进入 build_temp 目录)
    #   zip -r ...          (递归打包)
    #   ../$RELEASE_DIR/... (将包输出到上一级的 release 目录)
    #   "$PACKAGE_NAME"     (要打包的文件夹)
    (cd $BUILD_DIR && zip -r ../$RELEASE_DIR/"${PACKAGE_NAME}.zip" "$PACKAGE_NAME")
  else
    # --- Linux: 创建 .tar.gz ---
    # (关键!) 确保 Linux 可执行文件具有可执行权限
    chmod +x "$STAGING_DIR/$EXE_NAME"
    
    # -C $BUILD_DIR     (先切换到 build_temp 目录再开始打包, 这样 tar 包中不含 "build_temp/" 路径)
    # -czvf             (c=创建, z=gzip压缩, v=显示过程, f=文件名)
    tar -C $BUILD_DIR -czvf $RELEASE_DIR/"${PACKAGE_NAME}.tar.gz" "$PACKAGE_NAME"
  fi

done

# --- 5. 清理 ---
echo "-------------------------------------"
echo "🧹 正在清理临时文件..."
rm -rf $BUILD_DIR

echo "✅ 全部完成!"
echo "您的 Release 包已生成在 '$RELEASE_DIR' 目录下:"
ls -l $RELEASE_DIR

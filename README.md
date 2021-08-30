# goc

[![Go Report Card](https://goreportcard.com/badge/github.com/qiniu/goc)](https://goreportcard.com/report/github.com/qiniu/goc)
![](https://github.com/qiniu/goc/workflows/ut-check/badge.svg)
![](https://github.com/qiniu/goc/workflows/style-check/badge.svg)
![](https://github.com/qiniu/goc/workflows/e2e%20test/badge.svg)
![Build Release](https://github.com/qiniu/goc/workflows/Build%20Release/badge.svg)
[![codecov](https://codecov.io/gh/qiniu/goc/branch/master/graph/badge.svg)](https://codecov.io/gh/qiniu/goc)
[![GoDoc](https://godoc.org/github.com/qiniu/goc?status.svg)](https://godoc.org/github.com/qiniu/goc)

goc 是专为 Go 语言打造的一个综合覆盖率收集系统，尤其适合复杂的测试场景，比如系统测试时的代码覆盖率收集以及精准测试。

希望你们喜欢～

![Demo](docs/images/intro.gif)

以上是`goc`简介详细请到[https://github.com/qiniu/goc](https://github.com/qiniu/goc), 以下是本人基于`goc`的`7da52236182f82e0c8a5279d19071a9661309963`版本修改

- 新增`service`flag,支持编译时候，指定服务名可以不以二进制文件名作为服务名
- 新增`/v1/cover/report`接口输出服务的覆盖率报告(go tool cover --html 的结果)

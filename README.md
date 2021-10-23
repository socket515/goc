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
- 修复go mod replace 相对路径编译失败,解决方案`-mod=vendor`模式下，不需要更新`go.mod文件`
- 新增`/v1/cover/keepalive`接口,用于维持心跳，删除过期的service
- 新增`/v1/cover/metrics`接口,获取服务单元测试覆盖率统计数据
- `sever`新增`logfile`选项可以指定打log的路径

# 使用

 goc本地编译环境需要本地安装有`go`,最低支持版本`go 1.16`, goc center不需要有只需要有编译好二进制文件即可
 
## 安装

 ```bash
 git clone https://github.com/socket515/goc
 cd goc
 go install
 ```

## 编译

 编译需要获取运行时单元测试的服务
 ```bash
 goc build --buildflags=${buildflags} --service=${service_name} --center=${center}
 # 参数介绍
 # buildflags: go build 时候的相关参数，入mod=vendor
 # service: 此服务的名字，会注册到center中
 # center: goc的center服务地址
 ```


## goc center新增接口描述

### GET /v1/cover/report

|          |                  |
| -------- | ---------------- |
| 接口类型 | http             |
| 方法     | GET              |
| URI      | /v1/cover/report |

请求参数

| 参数    | 类型   | 描述     |
| ------- | ------ | -------- |
| module | string | 　goc 编译时候service指定的名字 |
| format | string | 　输出格式:(html或pkg) |
| force | string | 　1 表示不管module中服务是否有已经挂了 |
| coverfile | repeated string | 　只看这些文件 |
| skipfile | repeated string | 　跳过这些文件 |


```bash
curl http://10.203.57.119:7777/v1/cover/report?module=helloword&format=pkg
{
    "helloworld": "100.00%",
    "helloworld/cmd": "65.62%",
    "helloworld/common": "55.46%",
    "helloworld/component/fb": "0.00%",
    "helloworld/component/fieldstatistic": "36.73%",
    "helloworld/component/lenlimit": "75.00%",
    "helloworld/component/lock": "78.12%",
    "helloworld/component/playerconfig": "94.44%",
    "helloworld/component/ratelimiter": "0.00%",
    "helloworld/component/strmap": "29.41%",
    "helloworld/component/svc_user": "61.11%",
    "helloworld/component/token": "0.00%",
    "helloworld/handler/common/dispatcher": "72.22%",
    "helloworld/handler/common/gate": "38.46%",
    "helloworld/handler/common/gmgate": "40.20%",
    "helloworld/handler/common/org": "64.22%",
    "helloworld/project/alice": "72.28%",
    "helloworld/handler/common/user": "63.21%",
    "helloworld/project/barb": "66.67%",
    "total": "60.21%"
}
```

### GET /v1/cover/metrics
 
 获取最近服务单元测试情况(最多获取一天)

|          |                  |
| -------- | ---------------- |
| 接口类型 | http             |
| 方法     | GET              |
| URI      | /v1/cover/metrics |

请求参数

| 参数    | 类型   | 描述     |
| ------- | ------ | -------- |
| module | string | 　goc 编译时候service指定的名字 |
| beg | int | 时间范围,开始的时间戳(单位秒) |
| end | int | 　时间范围,结束的时间戳(单位秒) |

```bash
curl http://10.203.57.119:7777/v1/cover/metrics?module=helloworld&beg=1631845155&end=1631845215

{
    "data":[
        {
            "name":"helloworld",
            "covered":3138,
            "total":5212,
            "cover_rate":60.20721412125864,
            "pkg_data":{
                "helloworld/project/alice":{
                    "covered":902,
                    "total":1248,
                    "cover_rate":72.27564102564102
                },
                "helloworld/project/jack":{
                    "covered":445,
                    "total":704,
                    "cover_rate":63.21022727272727
                },
                "helloworld/project/barb":{
                    "covered":2,
                    "total":3,
                    "cover_rate":66.66666666666667
                }
            },
            "ts":1631845155
        },
        {
            "name":"helloworld",
            "covered":3138,
            "total":5212,
            "cover_rate":60.20721412125864,
            "pkg_data":{
                "helloworld/project/alice":{
                    "covered":902,
                    "total":1248,
                    "cover_rate":72.27564102564102
                },
                "helloworld/project/jack":{
                    "covered":445,
                    "total":704,
                    "cover_rate":63.21022727272727
                },
                "helloworld/project/barb":{
                    "covered":2,
                    "total":3,
                    "cover_rate":66.66666666666667
                }
            },
            "ts":1631845215
        }
    ]
}
```

### GET /goc-coverage-report

 获取最近服务单元测试变化表(最多获取一天)

|          |                  |
| -------- | ---------------- |
| 接口类型 | http             |
| 方法     | GET              |
| URI      | /v1/cover/metrics |

请求参数

| 参数    | 类型   | 描述     |
| ------- | ------ | -------- |
| module | []string | 　goc 编译时候service指定的名字,可以指定多个 |
| beg | int | 时间范围,开始的时间戳(单位秒) |
| end | int | 　时间范围,结束的时间戳(单位秒) |


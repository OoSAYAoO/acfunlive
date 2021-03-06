// AcFun直播通知和下载助手
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 运行程序所在文件夹
var exeDir string

type control int

// 控制信息
const (
	startCycle control = iota
	stopCycle
	liveOff
	stopRecord
	quit
)

// 主播的管道信息
type controlMsg struct {
	s streamer
	c control
}

// 主播的信息结构
type sMsg struct {
	ch          chan controlMsg    // 控制信息
	rec         record             // 下载信息
	recording   bool               // 是否正在下载
	modify      bool               // 是否被修改设置
	danmuCancel context.CancelFunc // 用来停止下载弹幕
}

// sMsg的map
var msgMap struct {
	sync.Mutex
	msg map[int]*sMsg
}

// main()的管道
var mainCh chan controlMsg

// main()的ctx
var mainCtx context.Context

// 程序是否处于监听状态
var isListen *bool

// 程序是否启动web服务
var isWebServer *bool

// 储存日志
var logString strings.Builder

// 可以同步输出的logger
var logger = log.New(os.Stdout, "", log.LstdFlags)

// 检查错误
func checkErr(err error) {
	if err != nil {
		lPrintErr(err)
		panic(err)
	}
}

// 获取时间
func getTime() string {
	t := time.Now()
	timeStr := fmt.Sprintf("%d-%02d-%02d %02d:%02d:%02d", t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second())
	return timeStr
}

// 打印带时间戳的log信息
func lPrintln(msg ...interface{}) {
	logger.Println(msg...)
	// 同时输出日志到web服务
	fmt.Fprintln(&logString, msg...)
}

// 打印错误信息
func lPrintErr(msg ...interface{}) {
	e := make([]interface{}, 1)
	e[0] = "[ERROR]"
	msg = append(e, msg...)
	lPrintln(msg...)
}

// 打印警告信息
func lPrintWarn(msg ...interface{}) {
	e := make([]interface{}, 1)
	e[0] = "[WARN]"
	msg = append(e, msg...)
	lPrintln(msg...)
}

// 将int转换为字符串
var itoa = strconv.Itoa

// 将字符串转换为int
var atoi = strconv.Atoi

// 将UID转换成字符串
func (s streamer) itoa() string {
	return itoa(s.UID)
}

// 返回ID（UID）形式的字符串
func (s streamer) longID() string {
	return s.Name + "（" + s.itoa() + "）"
}

// 尝试删除msgMap.msg里的键
func deleteMsg(uid int) {
	streamers.Lock()
	defer streamers.Unlock()
	msgMap.Lock()
	defer msgMap.Unlock()
	_, oks := streamers.crt[uid]
	m, okm := msgMap.msg[uid]
	// 删除临时下载的msg
	if !oks && okm && !m.recording && (m.danmuCancel == nil) {
		delete(msgMap.msg, uid)
	}
}

// 命令行参数处理
func argsHandle() {
	const usageStr = "本程序用于AcFun主播的开播提醒和自动下载直播"

	shortHelp := flag.Bool("h", false, "输出本帮助信息")
	longHelp := flag.Bool("help", false, "输出本帮助信息")
	isListen = flag.Bool("listen", false, "监听主播的直播状态，自动通知主播的直播状态或下载主播的直播，运行过程中如需更改设置又不想退出本程序，可以直接输入相应命令或手动修改设置文件"+liveFile)
	isWebServer = flag.Bool("web", false, "启动web服务，可以通过 "+address(config.WebPort)+" 来查看状态和发送命令，需要listen参数")
	isCoolq = flag.Bool("coolq", false, "使用酷Q发送直播通知到指定QQ或QQ群，需要事先设置并启动酷Q")
	isListLive := flag.Bool("listlive", false, "列出正在直播的主播")
	addNotifyUID := flag.Uint("addnotify", 0, "订阅指定主播的开播提醒，需要主播的uid（在主播的网页版个人主页查看）")
	delNotifyUID := flag.Uint("delnotify", 0, "取消订阅指定主播的开播提醒，需要主播的uid（在主播的网页版个人主页查看）")
	addRecordUID := flag.Uint("addrecord", 0, "自动下载指定主播的直播视频，需要主播的uid（在主播的网页版个人主页查看）")
	delRecordUID := flag.Uint("delrecord", 0, "取消自动下载指定主播的直播视频，需要主播的uid（在主播的网页版个人主页查看）")
	addDanmuUID := flag.Uint("adddanmu", 0, "自动下载指定主播的直播弹幕，需要主播的uid（在主播的网页版个人主页查看）")
	delDanmuUID := flag.Uint("deldanmu", 0, "取消自动下载指定主播的直播弹幕，需要主播的uid（在主播的网页版个人主页查看）")
	getStreamURL := flag.Uint("getdlurl", 0, "查看指定主播是否在直播，如在直播输出其直播源地址，需要主播的uid（在主播的网页版个人主页查看）")
	startRecord := flag.Uint("startrecord", 0, "临时下载指定主播的直播视频，需要主播的uid（在主播的网页版个人主页查看）")
	startDlDanmu := flag.Uint("startdanmu", 0, "临时下载指定主播的直播弹幕，需要主播的uid（在主播的网页版个人主页查看）")
	startRecDanmu := flag.Uint("startrecdan", 0, "临时下载指定主播的直播视频和弹幕，需要主播的uid（在主播的网页版个人主页查看）")
	flag.Parse()

	if flag.NArg() != 0 || flag.NFlag() == 0 {
		lPrintErr("请输入正确的参数")
		fmt.Println(usageStr)
		flag.PrintDefaults()
	} else {
		if *shortHelp || *longHelp {
			fmt.Println(usageStr)
			flag.PrintDefaults()
		}
		if *isWebServer || *isCoolq {
			if *isListen != true {
				lPrintErr("web和coolq参数需要和listen参数一起运行")
				os.Exit(1)
			}
		}
		if *isListLive {
			listLive()
		}
		if *addNotifyUID != 0 {
			addNotify(int(*addNotifyUID))
		}
		if *delNotifyUID != 0 {
			delNotify(int(*delNotifyUID))
		}
		if *addRecordUID != 0 {
			addRecord(int(*addRecordUID))
		}
		if *delRecordUID != 0 {
			delRecord(int(*delRecordUID))
		}
		if *addDanmuUID != 0 {
			addDanmu(int(*addDanmuUID))
		}
		if *delDanmuUID != 0 {
			delDanmu(int(*delDanmuUID))
		}
		if *getStreamURL != 0 {
			printStreamURL(int(*getStreamURL))
		}
		if *startRecord != 0 {
			startRec(int(*startRecord), false)
		}
		if *startDlDanmu != 0 {
			startDanmu(int(*startDlDanmu))
		}
		if *startRecDanmu != 0 {
			startRecDan(int(*startRecDanmu))
		}
	}
}

// 检查config.json里的配置
func checkConfig() {
	switch {
	case config.Source != "hls" && config.Source != "flv":
		lPrintErr(configFile + "里的Source必须是hls或flv")
		os.Exit(1)
	case config.WebPort < 1024 || config.WebPort > 65535:
		lPrintErr(configFile + "里的WebPort必须大于1023且少于65536")
		os.Exit(1)
	}
}

// 程序初始化
func initialize() {
	// 避免 initialization loop
	boolDispatch["startweb"] = startWeb
	boolDispatch["startcoolq"] = startCoolq

	exePath, err := os.Executable()
	checkErr(err)
	exeDir = filepath.Dir(exePath)
	logoFileLocation = filepath.Join(exeDir, logoFile)
	liveFileLocation = filepath.Join(exeDir, liveFile)
	configFileLocation = filepath.Join(exeDir, configFile)

	if _, err := os.Stat(logoFileLocation); os.IsNotExist(err) {
		lPrintln("下载AcFun的logo")
		fetchAcLogo()
	}

	if !isConfigFileExist(liveFile) {
		err = ioutil.WriteFile(liveFileLocation, []byte("[]"), 0644)
		checkErr(err)
		lPrintln("创建设置文件" + liveFile)
	}
	if !isConfigFileExist(configFile) {
		data, err := json.MarshalIndent(config, "", "    ")
		checkErr(err)
		err = ioutil.WriteFile(configFileLocation, data, 0644)
		checkErr(err)
		lPrintln("创建设置文件" + configFile)
	}

	msgMap.msg = make(map[int]*sMsg)
	streamers.crt = make(map[int]streamer)
	streamers.old = make(map[int]streamer)
	loadLiveConfig()
	loadConfig()
	checkConfig()

	for uid, s := range streamers.crt {
		streamers.old[uid] = s
	}

	fetchAllRooms()
}

func main() {
	initialize()

	argsHandle()

	if *isListen {
		if len(streamers.crt) == 0 {
			lPrintWarn("请订阅指定主播的开播提醒或自动下载，运行 acfunlive -h 查看帮助")
		}

		lPrintln("本程序开始监听主播的直播状态")

		mainCh = make(chan controlMsg, 20)

		for _, s := range streamers.crt {
			go s.cycle()
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		mainCtx = ctx

		go cycleConfig(ctx)

		lPrintln("现在可以输入命令修改设置，输入 help 查看全部命令的解释")
		go handleInput()

		if *isWebServer {
			lPrintln("启动web服务，现在可以通过 " + address(config.WebPort) + " 来查看状态和发送命令")
			go httpServer()
		}

		if *isCoolq {
			lPrintln("尝试通过 " + config.Coolq.CqhttpWSAddr + " 连接酷Q")
			initCoolq()
		}

		go cycleFetch(ctx)

		for {
			select {
			case msg := <-mainCh:
				switch msg.c {
				case startCycle:
					go msg.s.cycle()
				case quit:
					// 停止web服务
					if *isWebServer {
						lPrintln("正在停止web服务")
						if err := srv.Shutdown(ctx); err != nil {
							lPrintErr("web服务关闭错误：", err)
						}
					}
					// 结束所有mainCtx的子ctx
					cancel()
					// 结束cycle()
					lPrintln("正在退出各主播的循环")
					msgMap.Lock()
					for _, m := range msgMap.msg {
						// 退出各主播的循环
						if m.ch != nil {
							m.ch <- msg
						}
						// 结束下载直播视频
						if m.recording {
							m.rec.ch <- stopRecord
							io.WriteString(m.rec.stdin, "q")
						}
						// 结束下载弹幕
						if m.danmuCancel != nil {
							m.danmuCancel()
						}
					}
					msgMap.Unlock()
					danglingRec.Lock()
					for _, rec := range danglingRec.records {
						rec.ch <- stopRecord
						io.WriteString(rec.stdin, "q")
					}
					danglingRec.Unlock()
					// 等待20秒，等待其他goroutine结束
					time.Sleep(20 * time.Second)
					lPrintln("本程序结束运行")
					return
				default:
					lPrintErr("未知controlMsg：", msg)
				}
			}
		}
	}
}

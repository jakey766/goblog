//AppName: goblog
//Version: V1.0.0
//User: marco
//Date: 2018/11/17

package logs

import (
	"fmt"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/orm"
	"goblog/src/utils/bizerror"
	"goblog/src/utils/datetime"
	"io"
	"log"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"time"
)

const (
	DEBUG = iota
	INFO
	WARN
	ERROR
	SYS
)

var Log *logs

var lock = new(sync.RWMutex)
var logChannel = make(chan string, 1e2)
var cutChannel = make(chan bool)

type logs struct {
	consoleMode bool
	fileMode    bool
	filePath    string
	fileName    string
	logLevel    int
	async       bool
	logger      *log.Logger
	writer      io.Writer
}

func InitLogs(cutFlag bool) *logs {
	if cutFlag {
		fmt.Printf("call cutFlag Log:%+v,cutFlag:%v ", Log, cutFlag)
	}
	if Log == nil || cutFlag {
		lock.Lock()
		defer lock.Unlock()
		if Log == nil || cutFlag {
			cfg := beego.AppConfig
			currentPath, err := os.Getwd()
			bizerror.Check(err)

			Log = new(logs)
			Log.consoleMode = cfg.DefaultBool("consoleMode", true)
			Log.fileMode = cfg.DefaultBool("fileMode", true)
			Log.filePath = cfg.DefaultString("filePath", currentPath+"/log/")
			Log.logLevel = cfg.DefaultInt("logLevel", 1)
			Log.async = cfg.DefaultBool("async", true)

			fmt.Printf("call cutFlag newLog:%+v ", Log)

			_, err = os.Stat(Log.filePath)
			if os.IsNotExist(err) {
				err := os.MkdirAll(Log.filePath, 0766)
				bizerror.CheckBizError(err, bizerror.BizError404002)
			}
			appname := cfg.DefaultString("appname", "goblog")
			Log.fileName = appname + ".log"

			//TODO
			logFile, err := os.OpenFile(Log.filePath+Log.fileName, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0766)
			multiWriter := io.MultiWriter(logFile, os.Stdout)
			Log.writer = multiWriter
			Log.logger = log.New(multiWriter, "["+appname+"] ", log.Ldate|log.Ltime|log.Lshortfile)
			Log.logger.SetFlags(log.Ldate | log.Lmicroseconds)
			Log.Sys("call init goblog currentPath:%v,logs:%+v", currentPath, Log)

			//orm日志输入
			orm.DebugLog = orm.NewLog(GetLogsWriter())

			if Log.async {
				go async()
			}
			go logCut(logFile)
		}
	}
	return Log
}

func GetLogsWriter() io.Writer {
	if Log == nil {
		InitLogs(false)
	}
	return Log.writer
}

func logCut(logFile *os.File) {
	defer RecoverLog()

	nowTime := time.Now()
	y, m, d := nowTime.Add(24 * time.Hour).Date()
	nextDay := time.Date(y, m, d, 0, 0, 0, 0, nowTime.Location())
	Log.Sys("call fileCut nowTime:%v,nextDay:%v", nowTime, nextDay)
	tm := time.NewTimer(time.Duration(nextDay.UnixNano() - nowTime.UnixNano() + 100))
	//tm := time.NewTimer(time.Duration(time.Second * 60))
	<-tm.C

	fmt.Printf("call lock.RLock() ")
	lock.RLock()

	oldFilePath := Log.filePath + Log.fileName
	newFilePath := Log.filePath + datetime.FormatTime(nowTime, datetime.FM_DATE) + ".log"
	Log.Sys("call fileCut oldFilePath:%v,newFilePath:%v", oldFilePath, newFilePath)

	fileInfo, err := os.Stat(oldFilePath)
	fmt.Printf("call os.Stat fileInfo:%v", fileInfo)
	if os.IsNotExist(err) {
		fmt.Printf("fmt call fileCut IsNotExist err:%v ", err)
		Log.Sys("call fileCut IsNotExist err:%v", err)
		bizerror.BizError404002.PanicError()
	} else {
		fmt.Printf("call os.Stat else ")
	}

	//关闭日志接收
	cutChannel <- true

	//关闭老文件
	fmt.Printf("call logFile.Close() ")
	bizerror.Check(logFile.Close())
	//重命名
	fmt.Printf("call logFile.Rename() ")
	bizerror.Check(os.Rename(oldFilePath, newFilePath))

	fmt.Printf("call logFile.RUnlock() ")
	lock.RUnlock()

	//重新初始化
	fmt.Printf("call logFile.InitLogs() ")
	InitLogs(true)
}

func formatLog(level, skip int) (msg string) {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return
	}
	_, filename := path.Split(file)
	msg = filename + ":" + strconv.Itoa(line)
	switch level {
	case SYS:
		msg += " [SYS] "
	case ERROR:
		msg += " [ERROR] "
	case WARN:
		msg += " [WARN] "
	case INFO:
		msg += " [INFO] "
	case DEBUG:
		msg += " [DEBUG] "
	default:
		msg += " [INFO] "
	}
	return
}

func (l *logs) Debug(format string, log ...interface{}) {
	if l.logLevel > DEBUG {
		return
	}
	write(formatLog(DEBUG, 2)+format, true, log...)
}

func (l *logs) Info(format string, log ...interface{}) {
	if l.logLevel > INFO {
		return
	}
	write(formatLog(INFO, 2)+format, true, log...)
}

func (l *logs) Warn(format string, log ...interface{}) {
	if l.logLevel > WARN {
		return
	}
	write(formatLog(WARN, 2)+format, true, log...)
}

func (l *logs) Error(format string, log ...interface{}) {
	if l.logLevel > ERROR {
		return
	}
	write(formatLog(ERROR, 2)+format, true, log...)
}

func (l *logs) Sys(format string, log ...interface{}) {
	if l.logLevel > SYS {
		return
	}
	write(formatLog(SYS, 2)+format, false, log...)
}

func write(msg string, lockFlag bool, log ...interface{}) {
	if Log.async && lockFlag {
		if len(log) > 0 {
			msg = fmt.Sprintf(msg, log...)
		}
		logChannel <- msg
	} else {
		if lockFlag {
			lock.Lock()
			defer lock.Unlock()
		}
		Log.logger.Printf(msg, log...)
	}
}

//异步输出日志
func async() {
	defer RecoverLog()
	if Log.async {
		Log.Sys("call async log start")
		endFlag := false
		for {
			select {
			case msg := <-logChannel:
				lock.Lock()
				Log.logger.Printf(msg)
				lock.Unlock()
			case cutMsg := <-cutChannel:
				endFlag = cutMsg
			}
			if endFlag {
				Log.Sys("call async cutChannel endFlag:%v", endFlag)
				break
			}
		}
	}
}

func RecoverLog() {
	if err := recover(); err != nil {
		fmt.Printf(" Printf call RecoverLog err:%v,stack:%v", err, string(debug.Stack()))
		write(formatLog(ERROR, 1)+"call RecoverLog err:%v,stack:%v", false, err, string(debug.Stack()))
	}
}

package main

import (
    "database/sql"
    "encoding/xml"
    "fmt"
    "github.com/eatmoreapple/openwechat"
	_ "github.com/mattn/go-sqlite3"
    "log"
    "math/rand"
    "os"
    "path/filepath"
    "regexp"
    "strconv"
    "strings"
    "time"
    "io"
    "net/http"
)
type Msg struct {
	XMLName xml.Name `xml:"msg"`
	AppMsg  appmsg   `xml:"appmsg"`
}

type TradeItem struct {
    ID             int     `db:"id"`             // 交易品的唯一标识符
    Seller         string  `db:"seller"`         // 卖家的用户名或唯一标识
    Buyers         string  `db:"buyers"` 
            // 买家的用户名或唯一标识，可能有多个，逗号分隔
    GroupID        string  `db:"group_id"`       // 交易所在的群聊ID
    ItemName       string  `db:"item_name"`      // 交易品名称
    Description    string  `db:"description"`    // 交易品描述，可选
    Price          float64 `db:"price"`          // 交易品价格
    Quantity       int     `db:"quantity"`       // 交易品数量
    ImageFileName  string  `db:"image_file_name"`// 交易品图片文件名，可选
}

type appmsg struct {
	Type      int    `xml:"type"`
	AppId     string `xml:"appid,attr"`
	SdkVer    string `xml:"sdkver,attr"`
	Title     string `xml:"title"`
	Des       string `xml:"des"`
	Action    string `xml:"action"`
	Content   string `xml:"content"`
	Url       string `xml:"url"`
	LowUrl    string `xml:"lowurl"`
	ExtInfo   string `xml:"extinfo"`
	WcpayInfo struct {
		PaySubType   int    `xml:"paysubtype"`
		FeeDesc      string `xml:"feedesc"`
		TranscationId string `xml:"transcationid"`
		TransferId    string `xml:"transferid"`
	} `xml:"wcpayinfo"`
}
// 初始化数据库和创建表结构
func initDB() *sql.DB {
	db, err := sql.Open("sqlite3", "..\\star_journal.db")
	if err != nil {
		log.Fatalf("打开数据库失败: %s\n", err)
	}

	// 创建充值记录表
	createRechargeRecordsTable := `
	CREATE TABLE IF NOT EXISTS recharge_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		amount REAL NOT NULL,
		recharge_code TEXT NOT NULL UNIQUE,
		used INTEGER NOT NULL DEFAULT 0  -- 0: 未使用, 1: 已使用
	);`
	if _, err := db.Exec(createRechargeRecordsTable); err != nil {
		log.Fatalf("创建充值记录表失败: %s\n", err)
	}
    // 创建 trade_items 表
    createTradeItemsTableSQL := `
    CREATE TABLE IF NOT EXISTS trade_items (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        seller TEXT NOT NULL,
        buyers TEXT,  
        group_id TEXT,  
        item_name TEXT NOT NULL,
        description TEXT,
        price REAL NOT NULL,
        quantity INTEGER NOT NULL,
        image_file_name TEXT  
    );`
    if _, err := db.Exec(createTradeItemsTableSQL); err != nil {
        log.Fatalf("创建 trade_items 表失败: %s\n", err)
    }
	// 创建 member_stars 表
    createMemberStarsTableSQL := `CREATE TABLE IF NOT EXISTS member_stars (
        GroupID TEXT NOT NULL,
        UserName TEXT NOT NULL,
        StarsCount INTEGER,
        PRIMARY KEY (GroupID, UserName)
    );`
    _, err = db.Exec(createMemberStarsTableSQL)
    if err != nil {
        log.Fatalf("创建 member_stars 表失败: %s\n", err)
    }

    return db
}

func generateRechargeCode() string {
    rand.Seed(time.Now().UnixNano()) // 初始化随机数种子
    code := fmt.Sprintf("%d", rand.Int()) // 生成随机数作为充值码
    return code
}

// 从 XML 消息中提取转账金额
func extractAmountFromXML(content string) float64 {
	var msg Msg
	err := xml.Unmarshal([]byte(content), &msg)
	if err != nil {
		fmt.Printf("解析 XML 出错: %s\n", err)
		return 0
	}
	// 检查 paysubtype，如果是3，则忽略该转账
	if msg.AppMsg.WcpayInfo.PaySubType == 3 {
		return 0
	}
	// 提取金额字符串，并移除货币符号
	amountStr := regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`).FindString(msg.AppMsg.WcpayInfo.FeeDesc)

	// 将金额字符串转换为 float64 类型
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil {
		fmt.Printf("转换金额字符串为数字出错: %s\n", err)
		return 0
	}

	return amount
}

func main() {
	bot := openwechat.DefaultBot(openwechat.Desktop) // 使用桌面模式
	// 创建热存储容器对象，用于保存和加载登录会话信息
	reloadStorage := openwechat.NewFileHotReloadStorage("storage.json")
	defer reloadStorage.Close() // 确保在程序结束时关闭热存储容器

    
	// 尝试使用热登录
	err := bot.HotLogin(reloadStorage)
	if err != nil {
		fmt.Println("热登录失败，尝试扫码登录：", err)
		// 注册扫码登录的二维码回调
		bot.UUIDCallback = openwechat.PrintlnQrcodeUrl
		// 执行扫码登录
		if err := bot.Login(); err != nil {
			fmt.Println("扫码登录失败：", err)
			return
		}
	} else {
		fmt.Println("热登录成功")
	}
    self, err := bot.GetCurrentUser()
    if err != nil {
        fmt.Println("获取当前用户信息失败:", err)
        return
    }

	// 初始化数据库
	db := initDB()
	defer db.Close()
	// 获取所有的好友
	friends, err := self.Friends()
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("好友数量：", len(friends))

	// 获取所有的群组
	groups, err := self.Groups()
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("群组数量：", len(groups))
	// 注册消息处理函数
	bot.MessageHandler = func(msg *openwechat.Message) {
		if msg.IsSendByFriend() {
			handlePrivateMessage(msg, db)
		} else if msg.IsSendByGroup() {
            handleGroupMessage(msg, db, self) // 确保这里的self是*openwechat.Self类型的实例
		} else {
			handleOtherMessage(msg, db)
		}
	}

	// 阻塞主goroutine, 直到发生异常或者用户主动退出
	bot.Block()
}

// 处理私聊消息
func handlePrivateMessage(msg *openwechat.Message, db *sql.DB) {
    // 获取消息发送者的信息
    sender, err := msg.Sender()
    if err != nil {
        log.Printf("获取群信息失败: %s\n", err)
        return
    }  
    if msg.Content == "价格表" {
        // 从数据库中获取当前的赛事信息
        eventText := getCurrentEventFromDB(db)
        fmt.Println("查询到的价格信息:", eventText) // 添加日志
        msg.ReplyText(eventText)
        return
    }
    if msg.Content == "我的历史" {
        handleUserHistory(msg, db, sender.NickName)
        return
    }
    if msg.IsPicture() {
        // 假设 getPendingTradeItem 函数可以获取用户未添加图片的交易品
        tradeItem, err := getPendingTradeItem(db, sender.NickName)
        if err != nil || tradeItem == nil {
            fmt.Printf("未找到待添加图片的交易品。")
            return
        }
    
        imgData, err := msg.GetPicture() // 获取图片
        if err != nil {
            fmt.Printf("获取图片失败，请稍后重试。")
            return
        }
    
        // 假设 savePicture 函数保存图片并返回文件名
        fileName, err := savePicture("../jiaoyi", imgData)
        if err != nil {
            fmt.Printf("保存图片失败，请稍后重试。")
            return
        }
    
        // 假设 updateTradeItemImage 函数根据交易品ID更新图片文件名
        err = updateTradeItemImage(db, tradeItem.ID, fileName)
        if err != nil {
            fmt.Printf("更新交易品图片失败，请稍后重试。")
            return
        }
    
        msg.ReplyText(fmt.Sprintf("交易品%s的图片已更新，等待买家进群", tradeItem.ItemName))
    }
    if msg.IsTransferAccounts() {
		fmt.Printf(msg.Content)
        amount := extractAmountFromXML(msg.Content)
        fmt.Printf("收到转账，金额：%.2f\n", amount)
        if amount != 0{
			// 生成唯一的充值码
			rechargeCode := generateRechargeCode()
			
			// 将转账金额和充值码记录到数据库中
			insertRechargeRecord(db, amount, rechargeCode)
			
			// 向用户发送确认消息和兑换码
			msg.ReplyText(fmt.Sprintf("兑换码：%s", rechargeCode))
			msg.ReplyText(fmt.Sprintf("请复制上面这句话发送到微信群中获取星卷。"))
		}
    }
    
    if msg.Content == "帮助" {
        helpMessage := `命令指南:
    - "帮助": 显示此帮助信息。
    - "创建交易品，名称，价格，数量[，描述]": 创建一个新的交易品。描述为可选项。
    - "我的交易品": 查询你创建的交易品列表。
    - "交易区": 浏览当前可用的交易品列表。
    - "交易区：[]": 浏览当前[指定名字]可用的交易品列表。
    - "交易[交易ID]号": 交易[指定ID]号的交易品
    - "开始交易[交易ID]号，名称：[名称]，价格：[价格]，描述：[描述]": 在群聊中启动一个交易。请确保交易ID正确。
    - "兑换码：[充值码]": 使用兑换码完成充值交易支付。
    
    请根据指令格式发送消息，确保信息的正确性。`
    
        msg.ReplyText(helpMessage)
    }
    

    if strings.HasPrefix(msg.Content, "充值") {
        // 提取金额文本
        rechargeAmountMatch := regexp.MustCompile(`充值(\d+(\.\d+)?)`).FindStringSubmatch(msg.Content)
        if len(rechargeAmountMatch) > 1 {
            amountStr := rechargeAmountMatch[1]
            // 将提取的金额文本转换为 float64 类型
            amount, err := strconv.ParseFloat(amountStr, 64)
            if err != nil {
                msg.ReplyText("无法解析金额，请确保格式正确。例如：充值100")
                return
            }
            // 根据金额查询对应的充值码
            rechargeCode, err := getRechargeCodeByAmount(db, amount)
            if err != nil {
                msg.ReplyText("未找到对应金额的充值码，或已被使用。")
                return
            }
            msg.ReplyText(fmt.Sprintf("兑换码：%s，请复制上面这句话发送到微信群中获取星卷。", rechargeCode))
        }
    }
    switch {
    case strings.HasPrefix(msg.Content, "交易"):
        parts := strings.Split(msg.Content, "，")
        if len(parts) >= 4 && len(parts) <= 6 { // 允许4个到6个部分
            sellerName := parts[1]
            itemName := parts[2]
            price, err := strconv.ParseFloat(parts[3], 64)
            if err != nil {
                msg.ReplyText("价格格式不正确。请确保是数字。")
                return
            }
            quantity := 1 // 默认数量为1，如果用户没有指定
            if len(parts) > 4 {
                quantity, err = strconv.Atoi(parts[4])
                if err != nil {
                    msg.ReplyText("数量格式不正确。请确保是整数。")
                    return
                }
            }
            description := ""
            if len(parts) == 6 {
                description = parts[5]
            }
    
            // 插入新的交易品到数据库
            err = insertTradeItem(db, sellerName, itemName, description, price, quantity)
            if err != nil {
                msg.ReplyText(fmt.Sprintf("创建交易品失败：%v", err))
                return
            }
    
            msg.ReplyText(fmt.Sprintf("交易品%s创建完成！请创建新群聊并且将二维码发到此微信以便于进行交易", itemName))
        } else {
            msg.ReplyText("交易品信息不完整，请按照格式输入：'交易，卖家名称，名称，价格[，数量][，描述]'")
        }
        return    
    
	case strings.HasPrefix(msg.Content, "我的交易品"):
		// 处理 “我的交易品” 命令
		handleMyTradeItems(msg, db, sender.NickName)
        return
	case strings.HasPrefix(msg.Content, "交易区"):
		// 处理 “交易区” 命令
		handleTradeZone(msg, db)
        return
	case regexp.MustCompile(`^交易\d+`).MatchString(msg.Content):
		// 以 "交易" 开头且紧跟数字的特殊命令
		handleSpecificTradeItem(msg, db)
        return
	}
}

func handleGroupMessage(msg *openwechat.Message, db *sql.DB, self *openwechat.Self) {
    qun, err := msg.Sender()
    if err != nil {
        log.Printf("获取群信息失败: %s\n", err)
        return
    }
    // 获取消息发送者的信息
    sender, err := msg.SenderInGroup()
    if err != nil {
        log.Printf("获取群内消息发送者信息失败: %s\n", err)
        return
    }

    if msg.IsPicture() {
        // 假设 getPendingTradeItem 函数可以获取用户未添加图片的交易品
        tradeItem, err := getPendingTradeItem(db, sender.NickName)
        if err != nil || tradeItem == nil {
            fmt.Printf("未找到待添加图片的交易品。")
            return
        }
    
        imgData, err := msg.GetPicture() // 获取图片
        if err != nil {
            fmt.Printf("获取图片失败，请稍后重试。")
            return
        }
    
        // 假设 savePicture 函数保存图片并返回文件名
        fileName, err := savePicture("../jiaoyi", imgData)
        if err != nil {
            fmt.Printf("保存图片失败，请稍后重试。")
            return
        }
    
        // 假设 updateTradeItemImage 函数根据交易品ID更新图片文件名
        err = updateTradeItemImage(db, tradeItem.ID, fileName)
        if err != nil {
            fmt.Printf("更新交易品图片失败，请稍后重试。")
            return
        }
    
        msg.ReplyText(fmt.Sprintf("交易品%s的图片已更新，请耐心等待用户购买，输入”我的交易品“可以查看", tradeItem.ItemName))
    }
    
    // 处理 "开始交易" 指令
    tradeStartRegexpStr := `^开始交易(\d+)号，名称：(.+)，价格：(\d+(\.\d+)?)，描述：\s*(.*)$`
    tradeStartRe := regexp.MustCompile(tradeStartRegexpStr)
    tradeStartMatches := tradeStartRe.FindStringSubmatch(msg.Content)
    
    if len(tradeStartMatches) > 0 {
        tradeID, _ := strconv.Atoi(tradeStartMatches[1])  // 交易ID
        tradeName := tradeStartMatches[2]                // 交易名称
        tradePrice := tradeStartMatches[3]               // 交易价格
        // 更新交易项，将群聊ID绑定到此交易项
        err := bindTradeItemToGroup(db, tradeID, qun.UserName)
        if err != nil {
            // 错误处理
            msg.ReplyText("交易绑定失败，请重试。")
            return
        }

        // 回复提示信息
        msg.ReplyText("现在开始交易，请买家扫描下方二维码联系微信转账，进行下一步指示")
        // 准备发送给用户的文本消息
        personalizedMessage := fmt.Sprintf("您的交易开始，请您转账%s元购买%s，将返回下一步提示", tradePrice, tradeName)

        // 使用 replyToUser 函数向用户发送私聊文本消息
        replyToUser(self, sender.UserName, personalizedMessage)
        // 定义图片的绝对路径
        imagePath := "/root/chatgpt-on-wechat/group/image/名片.jpg"

        // 发送图片
        err = sendtupian(msg, imagePath)
        if err != nil {
            log.Printf("发送图片失败: %v\n", err)
            // 可选：如果图片发送失败，可以回复文本通知用户
            msg.ReplyText("发送图片失败，请稍后再试。")
        }

        return // 结束函数，防止执行后续的代码
    }

        // 处理 "兑换码" 指令
    rechargeCodeRegexpStr := `^兑换码：(\d+)$`
    rechargeCodeRe := regexp.MustCompile(rechargeCodeRegexpStr)
    rechargeCodeMatches := rechargeCodeRe.FindStringSubmatch(msg.Content)

    if len(rechargeCodeMatches) > 0 {
        rechargeCode := rechargeCodeMatches[1] // 兑换码

        // 查找消息发送者是否在群组中
        groups, err := self.Groups()
        if err != nil {
            fmt.Printf("获取群组列表失败: %v\n", err)
            return
        }

        senderInGroup := false
        for _, group := range groups {
            if group.UserName == qun.UserName {
                senderInGroup = true
                break
            }
        }

        if senderInGroup {
            return
        }

        // 兑换充值码
        amount, err := redeemRechargeCode(db, rechargeCode)
        if err != nil {
            msg.ReplyText(fmt.Sprintf("处理兑换码出错: %v", err))
            return
        }

        msg.ReplyText(fmt.Sprintf("用户已转账 %.2f 元，请进行下一步交易。", amount))
        return // 结束函数，防止执行后续的代码
    }

    if msg.IsTransferAccounts() {
        fmt.Printf(msg.Content)
        amount := extractAmountFromXML(msg.Content)
        fmt.Printf("收到群内转账，金额：%.2f\n", amount)
        
        // 查找消息发送者是否在群组中
        groups, err := self.Groups()
        if err != nil {
            fmt.Printf("获取群组列表失败: %v\n", err)
            return
        }

        senderInGroup := false
        for _, group := range groups {
            if group.UserName == qun.UserName {
                senderInGroup = true
                break
            }
        }
        if senderInGroup {
            return
        }
        msg.ReplyText(fmt.Sprintf("用户已转账 %.2f 元，请进行下一步交易。", amount))
    }
    switch {
    case strings.HasPrefix(msg.Content, "交易"):
        parts := strings.Split(msg.Content, "，")
        if len(parts) >= 3 && len(parts) <= 5 { // 允许3个到5个部分
            itemName := parts[1]
            price, err := strconv.ParseFloat(parts[2], 64)
            if err != nil {
                msg.ReplyText("价格格式不正确。请确保是数字。")
                return
            }
            quantity := 1 // 默认数量为1，如果用户没有指定
            if len(parts) > 3 {
                quantity, err = strconv.Atoi(parts[3])
                if err != nil {
                    msg.ReplyText("数量格式不正确。请确保是整数。")
                    return
                }
            }
            description := ""
            if len(parts) == 5 {
                description = parts[4]
            }
    
            // 插入新的交易品到数据库
            err = insertTradeItem(db, sender.NickName, itemName, description, price, quantity)
            if err != nil {
                msg.ReplyText(fmt.Sprintf("创建交易品失败：%v", err))
                return
            }
    
            msg.ReplyText(fmt.Sprintf("交易品%s创建完成！请创建新群聊并且将二维码发到此微信以便于进行交易", itemName))
        } else {
            msg.ReplyText("交易品信息不完整，请按照格式输入：'交易，名称，价格[，数量][，描述]'")
        }
        return    
	
	case strings.HasPrefix(msg.Content, "我的交易"):
		// 处理 “我的交易品” 命令
		handleMyTradeItems(msg, db, sender.NickName)
	
	case strings.HasPrefix(msg.Content, "交易区"):
		// 处理 “交易区” 命令
		handleTradeZone(msg, db)
	
	case regexp.MustCompile(`^交易\d+`).MatchString(msg.Content):
		// 以 "交易" 开头且紧跟数字的特殊命令
		handleSpecificTradeItem(msg, db)
    }	
}

// 处理其他消息，例如转账和红包消息
func handleOtherMessage(msg *openwechat.Message, db *sql.DB) {
   return
}

// 将转账金额和兑换码记录到数据库中
func insertRechargeRecord(db *sql.DB, amount float64, rechargeCode string) error {
    // 向数据库的充值记录表插入一条记录
    _, err := db.Exec("INSERT INTO recharge_records (amount, recharge_code, used) VALUES (?, ?, 0)", amount, rechargeCode)
    return err
}

func getRechargeCodeByAmount(db *sql.DB, amount float64) (string, error) {
    var rechargeCode string
    // 根据金额查询对应的充值码
    err := db.QueryRow("SELECT recharge_code FROM recharge_records WHERE amount = ? AND used = 0 LIMIT 1", amount).Scan(&rechargeCode)
    if err != nil {
        // 如果没有找到记录或查询出错，返回错误
        return "", err
    }
    // 如果找到记录，返回对应的充值码
    return rechargeCode, nil
}

func bindTradeItemToGroup(db *sql.DB, tradeItemID int, groupID string) error {
    // SQL 语句用于更新交易项，将其与群聊ID绑定
    updateSQL := `UPDATE trade_items SET group_id = ? WHERE id = ?`

    // 执行 SQL 更新
    _, err := db.Exec(updateSQL, groupID, tradeItemID)
    if err != nil {
        return err
    }

    return nil
}

func processRechargeCode(db *sql.DB, groupID string, rechargeCode int, buyerNickname string) error {
    // 首先，验证兑换码的有效性
    var amount float64
    checkCodeSQL := `SELECT amount FROM recharge_records WHERE recharge_code = ? AND used = 0`

    row := db.QueryRow(checkCodeSQL, rechargeCode)
    err := row.Scan(&amount)
    if err != nil {
        if err == sql.ErrNoRows {
            log.Printf("兑换码不存在或已被使用: %d", rechargeCode)
            return err  
        }
        log.Printf("查询兑换码时出错: %v", err)
        return err  
    }

    // 然后，找到绑定到该群组的交易项
    var tradeItemID int
    var price float64
    getTradeItemSQL := `SELECT id, price FROM trade_items WHERE group_id = ?`

    row = db.QueryRow(getTradeItemSQL, groupID)
    err = row.Scan(&tradeItemID, &price)
    if err != nil {
        if err == sql.ErrNoRows {
            log.Printf("找不到群组的交易项: %s", groupID)
            return err  
        }
        log.Printf("查询交易项时出错: %v", err)
        return err 
    }

    // 确保兑换码金额与交易项价格匹配
    if amount != price {
        log.Printf("兑换码金额 %.2f 与交易项价格 %.2f 不匹配", amount, price)
        return err  
    }

    // 最后，更新兑换码为已使用，更新交易项的买家信息
    updateCodeSQL := `UPDATE recharge_records SET used = 1 WHERE recharge_code = ?`
    _, err = db.Exec(updateCodeSQL, rechargeCode)
    if err != nil {
        log.Printf("更新兑换码为已使用时出错: %v", err)
        return err  
    }

    updateTradeItemSQL := `
    UPDATE trade_items
    SET buyers = CONCAT(IFNULL(buyers, ''), ?, '|'), quantity = quantity - 1
    WHERE id = ? AND quantity > 0
    `
    _, err = db.Exec(updateTradeItemSQL, buyerNickname, tradeItemID)
    if err != nil {
        log.Printf("更新交易项时出错: %v", err)
        return err  
    }

    return nil
}

func handleMyTradeItems(msg *openwechat.Message, db *sql.DB, seller string) {
    tradeItems, err := getUserTradeItems(db, seller)
    if err != nil {
        msg.ReplyText("获取交易品信息时发生错误，请稍后重试。")
        return
    }

    if len(tradeItems) == 0 {
        msg.ReplyText("您当前没有交易品。")
        return
    }

    for _, item := range tradeItems {
        buyers := strings.Split(item.Buyers, "|")
        soldOut := len(buyers)
        if buyers[0] == "" {
            soldOut = 0 // 如果买家字段为空，则认为没有售出任何单位
        }

        // 根据需要调整消息格式
        replyMsg := fmt.Sprintf("交易品ID：%d，名称：%s，价格：%.2f，描述：%s，数量：%d，已售出：%d",
            item.ID, item.ItemName, item.Price, item.Description, item.Quantity, soldOut)
        msg.ReplyText(replyMsg)
        // 如果有图片，也发送图片
        if item.ImageFileName != "" {
            // 假设 sendPicture 是一个发送图片消息的函数
            sendPicture(msg, item.ImageFileName)
        }
    }
}

func handleTradeZone(msg *openwechat.Message, db *sql.DB) {
    var content string

    // 检查命令是否以 "交易区：" 开头
    if strings.HasPrefix(msg.Content, "交易区：") {
        content = strings.TrimPrefix(msg.Content, "交易区：")
    } else if strings.HasPrefix(msg.Content, "交易区") { // 或者只是 "交易区"
        content = ""
    } else {
        // 如果既不是 "交易区：" 也不是 "交易区"，则可能是其他命令或消息
        return
    }

    tradeItems, err := getAvailableTradeItems(db, content) // content 可能是空字符串，表示不过滤名称
    if err != nil {
        msg.ReplyText("获取交易区信息时发生错误，请稍后重试。")
        return
    }

    if len(tradeItems) == 0 {
        msg.ReplyText("当前没有可用的交易品。")
        return
    }

    // 在消息开始处添加分隔符
    msg.ReplyText("————交易区————")

    for _, item := range tradeItems {
        // 使用指定格式构建回复消息
        replyMsg := fmt.Sprintf("%d号---%s（%s），价：%.2f", item.ID, item.ItemName, item.Description, item.Price)
        msg.ReplyText(replyMsg)
    }

    // 在消息结束处再次添加分隔符
    msg.ReplyText("————交易区————")
}

func handleSpecificTradeItem(msg *openwechat.Message, db *sql.DB) {
    var tradeID int
    _, err := fmt.Sscanf(msg.Content, "交易%d号", &tradeID)
    if err != nil {
        msg.ReplyText("指令格式错误，请按照 '交易[交易品ID]号' 的格式输入。")
        return
    }

    tradeItem, err := getTradeItemByID(db, tradeID)
    if err != nil {
        msg.ReplyText("获取交易品信息时发生错误，请稍后重试。")
        return
    }

    if tradeItem == nil {
        msg.ReplyText("未找到指定的交易品。")
        return
    }

    replyMsg := fmt.Sprintf("开始交易%d号，名称：%s，价格：%.2f，描述：%s", tradeItem.ID, tradeItem.ItemName, tradeItem.Price, tradeItem.Description)
    msg.ReplyText(replyMsg)
    // 如果有图片，也发送图片
    if tradeItem.ImageFileName != "" {
        sendPicture(msg, tradeItem.ImageFileName)
    }
    msg.ReplyText("扫描上面二维码进群，复制上面的话到群中进行下一步交易")
}

func getUserTradeItems(db *sql.DB, seller string) ([]TradeItem, error) {
    var tradeItems []TradeItem

    query := `SELECT id, item_name, description, price, quantity, image_file_name FROM trade_items WHERE seller = ? AND quantity > 0`
    rows, err := db.Query(query, seller)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    for rows.Next() {
        var item TradeItem
        if err := rows.Scan(&item.ID, &item.ItemName, &item.Description, &item.Price, &item.Quantity, &item.ImageFileName); err != nil {
            return nil, err
        }
        tradeItems = append(tradeItems, item)
    }

    if err = rows.Err(); err != nil {
        return nil, err
    }

    return tradeItems, nil
}

func getAvailableTradeItems(db *sql.DB, filter string) ([]TradeItem, error) {
    var tradeItems []TradeItem
    var query string
    var rows *sql.Rows
    var err error

    if filter == "" {
        query = `SELECT id, item_name, description, price, quantity, image_file_name FROM trade_items WHERE quantity > 0`
        rows, err = db.Query(query)
    } else {
        query = `SELECT id, item_name, description, price, quantity, image_file_name FROM trade_items WHERE quantity > 0 AND item_name LIKE ?`
        rows, err = db.Query(query, "%"+filter+"%")
    }

    if err != nil {
        return nil, err
    }
    defer rows.Close()

    for rows.Next() {
        var item TradeItem
        if err := rows.Scan(&item.ID, &item.ItemName, &item.Description, &item.Price, &item.Quantity, &item.ImageFileName); err != nil {
            return nil, err
        }
        tradeItems = append(tradeItems, item)
    }

    if err = rows.Err(); err != nil {
        return nil, err
    }

    return tradeItems, nil
}

func getTradeItemByID(db *sql.DB, tradeItemID int) (*TradeItem, error) {
    var item TradeItem

    query := `SELECT id, seller, item_name, description, price, quantity, image_file_name FROM trade_items WHERE id = ?`
    row := db.QueryRow(query, tradeItemID)
    if err := row.Scan(&item.ID, &item.Seller, &item.ItemName, &item.Description, &item.Price, &item.Quantity, &item.ImageFileName); err != nil {
        if err == sql.ErrNoRows {
            return nil, nil // 没有找到指定的交易品
        }
        return nil, err
    }

    return &item, nil
}

func sendPicture(msg *openwechat.Message, imageFileName string) error {
    // 假设所有图片都保存在 "../jiaoyi" 目录下
    filePath := filepath.Join("../jiaoyi", imageFileName)
    file, err := os.Open(filePath)
    if err != nil {
        return err
    }
    defer file.Close()

    _, err = msg.ReplyImage(file)
    return err
}

func insertTradeItem(db *sql.DB, seller, itemName, description string, price float64, quantity int) error {
    // 定义插入SQL语句
    insertStmt := `INSERT INTO trade_items (seller, buyers, group_id, item_name, description, price, quantity, image_file_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

    // 执行插入操作
    _, err := db.Exec(insertStmt, seller, "", "", itemName, description, price, quantity, "")
    if err != nil {
        log.Printf("插入交易品失败: %v\n", err)
        return err // 返回错误信息
    }

    return nil // 操作成功，返回nil
}

func savePicture(directory string, resp *http.Response) (string, error) {
    // 确保目录存在
    if err := os.MkdirAll(directory, 0755); err != nil {
        return "", err
    }

    // 生成唯一文件名
    fileName := fmt.Sprintf("%d.jpg", time.Now().UnixNano())
    filePath := filepath.Join(directory, fileName)

    // 打开文件写入
    file, err := os.Create(filePath)
    if err != nil {
        return "", err
    }
    defer file.Close()

    // 将响应体直接复制到文件中
    _, err = io.Copy(file, resp.Body)
    if err != nil {
        return "", err
    }

    // 确保响应体被关闭
    resp.Body.Close()

    return fileName, nil
}

func getPendingTradeItem(db *sql.DB, seller string) (*TradeItem, error) {
    var tradeItem TradeItem

    // 假设您的数据库表中 "image_file_name" 为空表示未添加图片
    query := `SELECT id, item_name FROM trade_items WHERE seller = ? AND image_file_name = '' LIMIT 1`
    row := db.QueryRow(query, seller)

    if err := row.Scan(&tradeItem.ID, &tradeItem.ItemName); err != nil {
        if err == sql.ErrNoRows {
            return nil, nil // 没有找到未添加图片的交易品
        }
        return nil, err
    }

    return &tradeItem, nil
}

func updateTradeItemImage(db *sql.DB, tradeItemID int, fileName string) error {
    updateStmt := `UPDATE trade_items SET image_file_name = ? WHERE id = ?`
    _, err := db.Exec(updateStmt, fileName, tradeItemID)
    return err
}

func sendtupian(msg *openwechat.Message, imagePath string) error {
    // 打开图片文件
    file, err := os.Open(imagePath)
    if err != nil {
        return err
    }
    defer file.Close()

    // 发送图片消息
    _, err = msg.ReplyImage(file)
    return err
}

func replyToUser(self *openwechat.Self, weChatUserName, content string) {
    friends, err := self.Friends()
    if err != nil {
        fmt.Printf("获取好友列表失败: %v\n", err)
        return
    }

    found := false
    for _, friend := range friends {
        if friend.UserName == weChatUserName {
            if _, err := friend.SendText(content); err != nil {
                fmt.Printf("向用户 [%s] 发送回复失败: %v\n", weChatUserName, err)
            } else {
                fmt.Printf("成功向用户 [%s] 发送回复\n", weChatUserName)
                found = true
            }
            break // 找到用户，跳出循环
        }
    }

    if !found {
        fmt.Printf("未找到用户 [%s]\n", weChatUserName)
    }
}

func handleUserHistory(msg *openwechat.Message, db *sql.DB, weChatUser string) {
    query := `
    SELECT CreatedAt, StarCost, GameID, Stars, FinalRank, IsActive, OrderType
    FROM Bridges
    WHERE GroupUserNickName = ?
    ORDER BY OrderType, CreatedAt DESC`
    rows, err := db.Query(query, weChatUser)
    if err != nil {
        msg.ReplyText(fmt.Sprintf("获取历史记录时出错: %v", err))
        return
    }
    defer rows.Close()
    type OrderTypeStats struct {
        TotalStarCost int
        TotalStars    int
    }
    orderTypeStats := make(map[string]OrderTypeStats)
    historyMessages := make(map[string][]string)
    for rows.Next() {
        var createdAt time.Time
        var starCost, stars int
        var gameID, finalRank, orderType string
        var isActive bool

        err := rows.Scan(&createdAt, &starCost, &gameID, &stars, &finalRank, &isActive, &orderType)
        if err != nil {
            log.Printf("读取历史记录时出错: %v\n", err)
            continue
        }
        // 累计星卷和星数
        stats := orderTypeStats[orderType]
        stats.TotalStarCost += starCost
        stats.TotalStars += stars
        orderTypeStats[orderType] = stats
        status := "已结束"
        if isActive {
            status = "正在进行中"
        }

        historyMessage := fmt.Sprintf("%s(使用%d星卷)\n%s--%d星[%s]$%s", createdAt.Format("2006-01-02 15:04:05"), starCost, gameID, stars, finalRank, status)
        historyMessages[orderType] = append(historyMessages[orderType], historyMessage)
    }

    if len(historyMessages) == 0 {
        msg.ReplyText("没有找到历史记录。")
        return
    }
    underline := strings.Repeat("—", 12)  // 设置下划线长度为80字符
    var response strings.Builder
    for orderType, messages := range historyMessages {
        stats := orderTypeStats[orderType]
        // 在每个订单类型的消息上方添加总计统计
        response.WriteString(fmt.Sprintf("%s:\n♥♥总使用星卷: %d♥♥\n🚗🚗总摘星: %d🚗🚗\n", 
                                         orderType, stats.TotalStarCost, stats.TotalStars))
        for _, message := range messages {
            response.WriteString(message + "\n")
            response.WriteString(underline + "\n")  // 每条记录后添加设定长度的下划线
        }
        response.WriteString("\n") // 在每个订单类型之间添加额外的空行
    }
    msg.ReplyText(response.String())
}

func getCurrentEventFromDB(db *sql.DB) string {
    var eventText string
    err := db.QueryRow("SELECT event_text FROM current_event WHERE id = 1").Scan(&eventText)
    if err != nil {
        fmt.Printf("Error while fetching event from DB: %s\n", err) // 更正为Printf
        return "暂无赛事信息"
    }
    return eventText
}

// 兑换充值码
func redeemRechargeCode(db *sql.DB, code string) (float64, error) {
    var amount float64
    var used int

    // 查询充值码对应的金额和使用状态
    err := db.QueryRow("SELECT amount, used FROM recharge_records WHERE recharge_code = ?", code).Scan(&amount, &used)
    if err != nil {
        if err == sql.ErrNoRows {
            return 0, fmt.Errorf("充值码不存在")
        }
        return 0, fmt.Errorf("查询充值码出错: %s", err)
    }

    if used != 0 {
        return 0, fmt.Errorf("充值码已被使用")
    }

    // 将充值码标记为已使用
    _, err = db.Exec("UPDATE recharge_records SET used = 1 WHERE recharge_code = ?", code)
    if err != nil {
        return 0, fmt.Errorf("更新充值码状态失败: %s", err)
    }

    return amount, nil
}
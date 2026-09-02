package video

// ChunkSize 定义了默认的分片大小：5MB
// 在计算机中，5 << 20 表示 5 左移 20 位，即 5 * 2^20 = 5MB
const ChunkSize = 5 << 20 // 5 MB

// ChunkUploadSession 代表一次分片上传的“会话状态”
// 前端开始上传时，后端会创建一个这样的 session，用来记录当前文件的上传进度
type ChunkUploadSession struct {
	UploadID     string `json:"upload_id"`     // 此次上传任务的唯一标识符
	AccountID    uint   `json:"account_id"`    // 发起上传的用户账号 ID
	Filename     string `json:"filename"`      // 原始文件名
	FileSize     int64  `json:"file_size"`     // 文件总大小（字节）
	ChunkSize    int64  `json:"chunk_size"`    // 每个分片的大小
	TotalChunks  int    `json:"total_chunks"`  // 总共需要上传的分片数量
	FileHash     string `json:"file_hash"`     // 整个文件的哈希值（用于实现“秒传”和校验完整性）
	UploadedBits []bool `json:"uploaded_bits"` // 核心：分片上传进度位图。第 i 个元素为 true，代表第 i 个分片已成功上传
}

// UploadedChunks 方法：获取所有已上传成功的分片索引
func (s *ChunkUploadSession) UploadedChunks() []int {
	var indices []int
	// 遍历位图，记录所有为 true 的索引
	for i, uploaded := range s.UploadedBits {
		if uploaded {
			indices = append(indices, i)
		}
	}
	return indices
}

// IsComplete 方法：检查文件是否已经全部上传完成
func (s *ChunkUploadSession) IsComplete() bool {
	// 只要发现有一个分片还是 false，就说明没传完
	for _, b := range s.UploadedBits {
		if !b {
			return false
		}
	}
	return true
}

// InitChunkUploadRequest 是前端发起“初始化分片上传”时发送的请求参数
type InitChunkUploadRequest struct {
	Filename    string `json:"filename" binding:"required"`           // 文件名
	FileSize    int64  `json:"file_size" binding:"required,min=1"`    // 文件总大小，必须大于 1 字节
	ChunkSize   int64  `json:"chunk_size" binding:"required,min=1"`   // 前端设定的分片大小
	TotalChunks int    `json:"total_chunks" binding:"required,min=1"` // 总共有多少个分片
	FileHash    string `json:"file_hash" binding:"required"`          // 文件哈希，后端可用此查询是否有相同文件
}

// UploadChunkRequest 是前端“上传单个分片”时发送的请求参数
type UploadChunkRequest struct {
	UploadID   string `form:"upload_id" binding:"required"`  // 上传任务 ID（通常在 query 参数中携带）
	ChunkIndex int    `form:"chunk_index" binding:"min=0"`   // 当前正在上传的分片索引（从 0 开始）
	ChunkHash  string `form:"chunk_hash" binding:"required"` // 当前分片的哈希，用于校验分片完整性
}

// ChunkStatusRequest 是前端“查询上传进度”时发送的请求参数
type ChunkStatusRequest struct {
	UploadID string `json:"upload_id" binding:"required"` // 上传任务 ID
}

// CompleteChunkUploadRequest 是前端通知后端“分片全部上传完毕，请求合并”时发送的请求参数
type CompleteChunkUploadRequest struct {
	UploadID string `json:"upload_id" binding:"required"` // 上传任务 ID
}

// Package meta は、aibow の MCP のクライアント(mcp/client)とサーバー(mcp/server)の両方で使う、_meta の名前。
//
// MCP のメッセージの _meta には、決まりごとに無い情報を自由に足せる(名前は「どこの・何か」が分かる形にする決まり)。
// aibow はサーバーからの確認(elicitation)に、確認のカードの中身を入れて送る。
// 両方で同じ名前を使うので、ここに1つだけ書く(internal なので aibow の外からは使えない)。
package meta

// Preview は、確認(elicitation)の _meta に入れる確認の内容(tool.Preview。差分・コマンドなど)の名前。
// aibow のクライアントはこれがあれば、今の確認のカード(差分の色付け)で出す。ほかのクライアントは message の文を出す
const Preview = "aibow/preview"

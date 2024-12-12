# iupload

http file transfer tool.

## 上传文件
~~~
curl -X POST 127.0.0.1:44321/upload -F "file1=@x.tar.gz" -F "file2=@y.tar.gz"
~~~

## 下载文件
~~~
curl -O -J http://127.0.0.1:44321/download\?file\=x.tar.gz
wget http://127.0.0.1:44321/download\?file\=y.tar.gz
~~~
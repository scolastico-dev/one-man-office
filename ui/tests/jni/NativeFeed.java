package dev.scolastico.omo.notifications;

import java.io.*;
import java.nio.file.*;

// A headless JVM adapter for the production JNI exports. Android's real
// DeliverySink/Keystore adapter is exercised separately by the Kotlin tests.
public final class NativeFeed {
    private native String pollFeed(String config, String root, String secret, DeliverySink sink, boolean baseline);
    public static final class DeliverySink {
        final Path file;
        final String destination;
        int saves;
        int delivered;
        boolean blocked;
        DeliverySink(Path file, String destination) { this.file=file; this.destination=destination; }
        public String loadJournal() throws IOException { return Files.exists(file) ? Files.readString(file) : ""; }
        public boolean saveJournal(String value) throws IOException {
            // Fail exactly the authenticated batch save, after persisting request.
            if (++saves==2) throw new IOException("injected journal failure");
            Path staged=file.resolveSibling("journal.new");
            try (FileOutputStream stream=new FileOutputStream(staged.toFile())) {
                stream.write(value.getBytes(java.nio.charset.StandardCharsets.UTF_8)); stream.getFD().sync();
            }
            Files.move(staged,file,StandardCopyOption.ATOMIC_MOVE,StandardCopyOption.REPLACE_EXISTING);
            return true;
        }
        public boolean deliver(String value) {
            if (blocked) return false;
            require(value.contains(destination),"wrong ticket/comment activation destination");
            delivered++;
            return true;
        }
    }
    static void require(boolean ok,String reason) { if(!ok) throw new AssertionError(reason); }
    public static void main(String[] args) throws Exception {
        System.load(args[0]);
        BufferedReader input=new BufferedReader(new InputStreamReader(System.in,java.nio.charset.StandardCharsets.UTF_8));
        String config=input.readLine(), root=input.readLine(), secret=input.readLine(), destination=input.readLine();
        DeliverySink sink=new DeliverySink(Path.of(root,"jni-journal"),destination);
        NativeFeed nativeFeed=new NativeFeed();
        require(nativeFeed.pollFeed(config,root,secret,sink,true).contains("\"more\":false"),"relay baseline called the direct feed");
        String reply=nativeFeed.pollFeed(config,root,secret,sink,false);
        require(reply.contains("\"error\""),"failed journal save was accepted");
        require(!sink.loadJournal().contains("comment:"),"failed batch save changed durable notices");
        reply=nativeFeed.pollFeed(config,root,secret,sink,false);
        require(reply.contains("\"more\":true"),"authenticated batch not saved");
        require(sink.loadJournal().contains(destination),"pending delivery not durable");
        String before=sink.loadJournal();sink.blocked=true;
        reply=nativeFeed.pollFeed(config,root,secret,sink,false);
        require(reply.contains("\"error\""),"blocked OS submission was accepted");
        require(before.equals(sink.loadJournal()),"blocked delivery changed receipt or pending notices");
        sink.blocked=false;
        reply=new NativeFeed().pollFeed(config,root,secret,sink,false);
        require(reply.contains("\"more\":false"),"receipt did not complete with authenticated empty");
        require(sink.delivered==1,"notice not delivered exactly once in this execution");
        require(sink.loadJournal().contains("\"receipt\":null"),"receipt not cleared");
        reply=new NativeFeed().pollFeed(config,root,secret,sink,false);
        require(reply.contains("\"more\":false") && sink.delivered==1,"recreated native feed replayed received comment");
        System.out.println("headless JNI notification receipt/delivery checks passed");
    }
}

alter table incident_comments rename column attachment_url to image_url;
alter table alert_comments rename column attachment_url to image_url;
alter table alerts rename column close_attachment_url to close_image_url;
